package Wiring

// scene_anchor_persist.go — the WRITE side of port-anchor-as-file-data.
//
// The read side (loader_tree.go readPorts → specPort.AnchorId) loads each port's ring-anchor
// index from `<root>/nodes/<id>/{inputs,outputs}/<port>.json`'s `anchorId` field. This file
// is the mirror: when the gesture FSM commits a ring-move (applyRingAnchor snaps the port to
// a ring-anchor index), Go debounced-persists that index back to the same port file,
// PRESERVING the other fields (name), so ring-move-then-reload round-trips.
//
// Same shape as the node-position persister: DEBOUNCED (a ring drag emits an anchor every
// pointermove), READ-MODIFY-WRITE (only `anchorId` is replaced), FIRE-AND-FORGET. root == ""
// (monolithic form / tests) disables it.

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"
)

// anchorKey identifies a port whose ring-anchor index changed.
type anchorKey struct {
	node    string
	port    string
	isInput bool
}

// anchorPersister coalesces rapid ring-anchor changes into a debounced read-modify-write of
// each affected port file's anchorId. Owned by MoveDispatch (armed by EnableEditPersist).
type anchorPersister struct {
	root     string
	debounce time.Duration
	debouncedPersister[map[anchorKey]int]
}

// schedule records the latest anchor index for a port and (re)arms the debounce timer.
func (p *anchorPersister) schedule(node, port string, isInput bool, anchorID int) {
	if p == nil || p.root == "" {
		return
	}
	p.mu.Lock()
	if p.pending == nil {
		p.pending = map[anchorKey]int{}
	}
	p.pending[anchorKey{node: node, port: port, isInput: isInput}] = anchorID
	v := p.pending
	p.mu.Unlock()
	p.arm(p.debounce, v, p.flush)
}

// flush writes every pending anchor to its port file and clears the pending set.
func (p *anchorPersister) flush() {
	pend, has := p.take()
	if !has || len(pend) == 0 {
		return
	}
	for k, anchorID := range pend {
		if err := writePortAnchor(p.root, k.node, k.port, k.isInput, anchorID); err != nil {
			logPersistErr("scene_anchor_persist", k.node+"/"+k.port, err)
		}
	}
	p.recordWrite()
}

// writePortAnchor sets ONLY the anchorId field of the port file, preserving the other fields
// (name). The port file must already exist (a placed port always has one).
func writePortAnchor(root, node, port string, isInput bool, anchorID int) error {
	if !safeTreePathComponent(node) || !safeTreePathComponent(port) {
		return fmt.Errorf("unsafe node/port %q/%q", node, port)
	}
	dir := "outputs"
	if isInput {
		dir = "inputs"
	}
	path := filepath.Join(root, "nodes", node, dir, port+".json")
	return entityReadModifyWrite(path, func(obj map[string]json.RawMessage) {
		b, _ := json.Marshal(anchorID)
		obj["anchorId"] = b
	})
}
