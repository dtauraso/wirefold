package Wiring

// scene_node_pos_persist.go — the WRITE side of node-position-as-file-data.
//
// The read side (loader.go toNodeGeom / loader_tree.go) loads each node's authoritative
// world center from its `x`/`y`/`z` fields — in the directory-tree form that is
// `<root>/nodes/<id>/meta.json`. This file is the mirror: when the gesture FSM commits a
// node drag (RootMove), Go debounced-persists every moved node's new center back to that
// same meta.json, PRESERVING all other fields (id/type/r), so drag-then-reload round-trips.
//
// Go owns persistence (MODEL.md): there is no TS→Go node-save; the write triggers off the
// RootMove the FSM already applies. It is DEBOUNCED (a drag emits a target every
// pointermove; we coalesce and write once motion settles) and READ-MODIFY-WRITE (meta.json
// also holds id/type/r — we replace only x/y/z). FIRE-AND-FORGET: it runs on the debounce
// timer's goroutine and logs on error; it never blocks the gesture.
//
// Only the directory-tree form has a per-node meta.json to write; for a monolithic
// topology.json (no per-node file) root is "" and the persister is a no-op.

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"
)

// nodePosPersister coalesces rapid node-center changes (a drag) into a debounced
// read-modify-write of each moved node's meta.json x/y/z. Owned by MoveDispatch (armed by
// EnableEditPersist). root == "" disables it (monolithic form / tests that never arm).
type nodePosPersister struct {
	root     string // tree root; per-node meta.json lives at <root>/nodes/<id>/meta.json
	debounce time.Duration
	debouncedPersister[map[string]vec3]
	// sceneCenter returns the current scene-sphere center, so each write can ALSO record the
	// node's SCENE POLAR (r,θ,φ = cart2polar(world − sceneCenter)) alongside x/y/z during the
	// polar-model migration (polar-model.md phase 2). nil → write cartesian only.
	sceneCenter func() vec3
}

// schedule records the latest center for a node and (re)arms the debounce timer. A
// continuous drag re-arms each call, so the burst coalesces into one write after settle.
func (p *nodePosPersister) schedule(id string, c vec3) {
	if p == nil || p.root == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pending == nil {
		p.pending = map[string]vec3{}
	}
	p.pending[id] = c
	p.has = true
	if p.timer == nil {
		p.timer = time.AfterFunc(p.debounce, p.flush)
	} else {
		p.timer.Reset(p.debounce)
	}
}

// flush writes every pending node center to its meta.json (read-modify-write, preserving
// other fields) and clears the pending set. Fire-and-forget: errors are logged, not returned.
func (p *nodePosPersister) flush() {
	pend, has := p.take()
	if !has || len(pend) == 0 {
		return
	}
	var sc vec3
	haveSC := false
	if p.sceneCenter != nil {
		sc, haveSC = p.sceneCenter(), true
	}
	for id, c := range pend {
		if err := writeNodePosition(p.root, id, c, sc, haveSC); err != nil {
			logPersistErr("scene_node_pos_persist", id, err)
		}
	}
	p.recordWrite()
}

// writeNodePosition sets the node's SCENE POLAR (r,θ,φ about the scene sphere center) in
// <root>/nodes/<id>/meta.json, preserving every other field (id, type, r) and DELETING any
// legacy cartesian x/y/z. Polar is the only stored position (polar-frame-rewrite.md phase 1:
// "persist writes scene-polar only, deletes x/y/z"). A scene center is required to express a
// polar position; without one the write is skipped (leaving the file untouched) rather than
// falling back to a cartesian center, which would reintroduce the very field the frame removes.
// The file must already exist (a node always has a meta.json); a missing/malformed file is
// reported rather than fabricated.
func writeNodePosition(root, id string, c vec3, sceneCenter vec3, haveSceneCenter bool) error {
	if !safeTreePathComponent(id) {
		return fmt.Errorf("unsafe node id %q", id)
	}
	if !haveSceneCenter {
		// No scene center → cannot express a polar position, and cartesian is not an
		// acceptable stored form. Skip rather than write a cartesian center.
		return nil
	}
	path := filepath.Join(root, "nodes", id, "meta.json")
	return entityReadModifyWrite(path, func(obj map[string]json.RawMessage) {
		setNum := func(key string, v float64) {
			b, _ := json.Marshal(v)
			obj[key] = b
		}
		sp := cart2polar(c.sub(sceneCenter))
		setNum("scenePolarR", sp.R)
		setNum("scenePolarTheta", sp.Theta)
		setNum("scenePolarPhi", sp.Phi)
		// Cartesian center is not a stored source of truth — drop any legacy fields.
		delete(obj, "x")
		delete(obj, "y")
		delete(obj, "z")
	})
}
