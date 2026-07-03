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
	"os"
	"path/filepath"
	"sync"
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
	mu       sync.Mutex
	pending  map[anchorKey]int
	timer    *time.Timer
	writes   int
}

// schedule records the latest anchor index for a port and (re)arms the debounce timer.
func (p *anchorPersister) schedule(node, port string, isInput bool, anchorID int) {
	if p == nil || p.root == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pending == nil {
		p.pending = map[anchorKey]int{}
	}
	p.pending[anchorKey{node: node, port: port, isInput: isInput}] = anchorID
	if p.timer == nil {
		p.timer = time.AfterFunc(p.debounce, p.flush)
	} else {
		p.timer.Reset(p.debounce)
	}
}

// flush writes every pending anchor to its port file and clears the pending set.
func (p *anchorPersister) flush() {
	p.mu.Lock()
	pend := p.pending
	p.pending = nil
	p.mu.Unlock()
	if len(pend) == 0 {
		return
	}
	for k, anchorID := range pend {
		if err := writePortAnchor(p.root, k.node, k.port, k.isInput, anchorID); err != nil {
			fmt.Fprintf(os.Stderr, "scene_anchor_persist: write %s/%s: %v\n", k.node, k.port, err)
		}
	}
	p.mu.Lock()
	p.writes++
	p.mu.Unlock()
}

// writePortAnchor sets ONLY the anchorId field of the port file, preserving the other fields
// (name). The port file must already exist (a placed port always has one).
func writePortAnchor(root, node, port string, isInput bool, anchorID int) error {
	dir := "outputs"
	if isInput {
		dir = "inputs"
	}
	path := filepath.Join(root, "nodes", node, dir, port+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	obj := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return err
	}
	b, _ := json.Marshal(anchorID)
	obj["anchorId"] = b
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
