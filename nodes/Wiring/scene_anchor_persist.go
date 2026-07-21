package Wiring

// scene_anchor_persist.go — the WRITE side of port-anchor-as-file-data.
//
// The read side (loader_tree.go readPorts → specPort.AnchorId) loads each port's ring-anchor
// index from `<root>/nodes/<id>/{inputs,outputs}/<port>.json`'s `anchorId` field. This file
// is the mirror: when the gesture FSM commits a ring-move (applyRingAnchor snaps the port to
// a ring-anchor index), Go persists that index back to the same port file, PRESERVING the
// other fields (name), so ring-move-then-reload round-trips.
//
// Same shape as the node-position persister: SYNCHRONOUS (schedule() writes immediately,
// inline on the stdin/gesture goroutine — see scene_persist.go's header comment for why the
// prior debounce was removed), READ-MODIFY-WRITE (only `anchorId` is replaced),
// FIRE-AND-FORGET. root == "" (monolithic form / tests) disables it.

import (
	"encoding/json"
	"fmt"
	"path/filepath"
)

// anchorPersister writes ring-anchor changes to their port file as they happen. Owned by
// MoveDispatch (armed by EnableEditPersist).
type anchorPersister struct {
	root   string
	writes int // count of completed writes (test observability); single writer
	// (applyRingAnchor runs only on the stdin/gesture goroutine) so no lock is needed.
}

// schedule writes the given anchor index to its port file synchronously.
func (p *anchorPersister) schedule(node, port string, isInput bool, anchorID int) {
	if p == nil || p.root == "" {
		return
	}
	if err := writePortAnchor(p.root, node, port, isInput, anchorID); err != nil {
		logPersistErr("scene_anchor_persist", node+"/"+port, err)
		return
	}
	p.writes++
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
