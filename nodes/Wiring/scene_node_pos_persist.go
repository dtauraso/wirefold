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
	debouncedPersister[map[string]polar]
}

// schedule records the latest SCENE POLAR for a node and (re)arms the debounce timer. A
// continuous drag re-arms each call, so the burst coalesces into one write after settle. The
// position is already polar (the mover's source of truth) — no cartesian involved.
func (p *nodePosPersister) schedule(id string, sp polar) {
	if p == nil || p.root == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pending == nil {
		p.pending = map[string]polar{}
	}
	p.pending[id] = sp
	p.has = true
	if p.timer == nil {
		p.timer = time.AfterFunc(p.debounce, p.flush)
	} else {
		p.timer.Reset(p.debounce)
	}
}

// flush writes every pending node's scene polar to its meta.json (read-modify-write, preserving
// other fields) and clears the pending set. Fire-and-forget: errors are logged, not returned.
func (p *nodePosPersister) flush() {
	pend, has := p.take()
	if !has || len(pend) == 0 {
		return
	}
	for id, sp := range pend {
		if err := writeNodePosition(p.root, id, sp); err != nil {
			logPersistErr("scene_node_pos_persist", id, err)
		}
	}
	p.recordWrite()
}

// writeNodePosition sets the node's SCENE POLAR (r,θ,φ about the scene sphere center) in
// <root>/nodes/<id>/meta.json, preserving every other field (id, type, r) and DELETING any
// legacy cartesian x/y/z. Polar is the only stored position (polar-frame-rewrite.md phase 1:
// "persist writes scene-polar only, deletes x/y/z"), and it is written straight from the polar
// source of truth — no cartesian conversion. The file must already exist (a node always has a
// meta.json); a missing/malformed file is reported rather than fabricated.
func writeNodePosition(root, id string, sp polar) error {
	if !safeTreePathComponent(id) {
		return fmt.Errorf("unsafe node id %q", id)
	}
	path := filepath.Join(root, "nodes", id, "meta.json")
	return entityReadModifyWrite(path, func(obj map[string]json.RawMessage) {
		setNum := func(key string, v float64) {
			b, _ := json.Marshal(v)
			obj[key] = b
		}
		setNum("scenePolarR", sp.R)
		setNum("scenePolarTheta", sp.Theta)
		setNum("scenePolarPhi", sp.Phi)
		// Cartesian center is not a stored source of truth — drop any legacy fields.
		delete(obj, "x")
		delete(obj, "y")
		delete(obj, "z")
	})
}
