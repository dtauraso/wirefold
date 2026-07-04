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
	"os"
	"path/filepath"
	"sync"
	"time"
)

// nodePosPersister coalesces rapid node-center changes (a drag) into a debounced
// read-modify-write of each moved node's meta.json x/y/z. Owned by MoveDispatch (armed by
// EnableEditPersist). root == "" disables it (monolithic form / tests that never arm).
type nodePosPersister struct {
	root     string // tree root; per-node meta.json lives at <root>/nodes/<id>/meta.json
	debounce time.Duration
	mu       sync.Mutex
	pending  map[string]vec3 // node id → latest center awaiting write
	timer    *time.Timer
	writes   int // count of completed flushes (test observability)
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
	if p.timer == nil {
		p.timer = time.AfterFunc(p.debounce, p.flush)
	} else {
		p.timer.Reset(p.debounce)
	}
}

// flush writes every pending node center to its meta.json (read-modify-write, preserving
// other fields) and clears the pending set. Fire-and-forget: errors are logged, not returned.
func (p *nodePosPersister) flush() {
	p.mu.Lock()
	pend := p.pending
	p.pending = nil
	p.mu.Unlock()
	if len(pend) == 0 {
		return
	}
	var sc vec3
	haveSC := false
	if p.sceneCenter != nil {
		sc, haveSC = p.sceneCenter(), true
	}
	for id, c := range pend {
		if err := writeNodePosition(p.root, id, c, sc, haveSC); err != nil {
			fmt.Fprintf(os.Stderr, "scene_node_pos_persist: write %s: %v\n", id, err)
		}
	}
	p.mu.Lock()
	p.writes++
	p.mu.Unlock()
}

// writeNodePosition sets ONLY the x/y/z fields of <root>/nodes/<id>/meta.json, preserving
// every other field (id, type, r). The file must already exist (a node always has a
// meta.json); a missing/malformed file is reported rather than fabricated.
func writeNodePosition(root, id string, c vec3, sceneCenter vec3, haveSceneCenter bool) error {
	path := filepath.Join(root, "nodes", id, "meta.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	obj := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return err
	}
	setNum := func(key string, v float64) {
		b, _ := json.Marshal(v)
		obj[key] = b
	}
	setNum("x", c.X)
	setNum("y", c.Y)
	setNum("z", c.Z)
	// Dual-write the SCENE POLAR (polar-model.md phase 2): the node's polar about the scene
	// sphere. Cartesian x/y/z stays for back-compat during migration; the polar fields become
	// authoritative once the load side prefers them (phase 2b).
	if haveSceneCenter {
		sp := cart2polar(c.sub(sceneCenter))
		setNum("scenePolarR", sp.R)
		setNum("scenePolarTheta", sp.Theta)
		setNum("scenePolarPhi", sp.Phi)
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
