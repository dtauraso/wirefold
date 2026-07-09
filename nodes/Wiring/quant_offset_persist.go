package Wiring

// quant_offset_persist.go — the WRITE side of the quantized-offset-as-file-data
// (quantized_layout.go PHASE 3). RootMove's drag-snap-to-grid path writes the dragged
// node's new (iTheta,iPhi,iR) into md.quantizedOffsets in memory; this file debounced-
// persists that same offset to <root>/nodes/<id>/meta.json, read-modify-write, mirroring
// scene_node_pos_persist.go's shape exactly (this is the offset-model analogue of that
// scenePolar persister — scenePolar itself is left untouched here; Phase 4 removes it).
//
// Only the directory-tree form has a per-node meta.json to write; for a monolithic
// topology.json (no per-node file) root is "" and the persister is a no-op.

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"
)

// quantOffsetPersister coalesces rapid quantized-offset changes (a drag) into a debounced
// read-modify-write of each moved node's meta.json quantITheta/quantIPhi/quantIR. Owned by
// MoveDispatch (armed by EnableEditPersist). root == "" disables it (monolithic form /
// tests that never arm).
type quantOffsetPersister struct {
	root     string // tree root; per-node meta.json lives at <root>/nodes/<id>/meta.json
	debounce time.Duration
	debouncedPersister[map[string]quantizedOffset]
}

// schedule records the latest quantized offset for a node and (re)arms the debounce timer.
// A continuous drag re-arms each call, so the burst coalesces into one write after settle.
func (p *quantOffsetPersister) schedule(id string, off quantizedOffset) {
	if p == nil || p.root == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pending == nil {
		p.pending = map[string]quantizedOffset{}
	}
	p.pending[id] = off
	p.has = true
	if p.timer == nil {
		p.timer = time.AfterFunc(p.debounce, p.flush)
	} else {
		p.timer.Reset(p.debounce)
	}
}

// flush writes every pending node's quantized offset to its meta.json (read-modify-write,
// preserving other fields) and clears the pending set. Fire-and-forget: errors are logged,
// not returned.
func (p *quantOffsetPersister) flush() {
	pend, has := p.take()
	if !has || len(pend) == 0 {
		return
	}
	for id, off := range pend {
		if err := writeNodeQuantOffset(p.root, id, off); err != nil {
			logPersistErr("quant_offset_persist", id, err)
		}
	}
	p.recordWrite()
}

// writeNodeQuantOffset sets the node's quantized offset (iTheta,iPhi,iR — parent is NOT
// stored; it is re-derived from the edge graph's spanning tree on every load) in
// <root>/nodes/<id>/meta.json, preserving every other field. The file must already exist
// (a node always has a meta.json); a missing/malformed file is reported rather than
// fabricated.
func writeNodeQuantOffset(root, id string, off quantizedOffset) error {
	if !safeTreePathComponent(id) {
		return fmt.Errorf("unsafe node id %q", id)
	}
	path := filepath.Join(root, "nodes", id, "meta.json")
	return entityReadModifyWrite(path, func(obj map[string]json.RawMessage) {
		setInt := func(key string, v int) {
			b, _ := json.Marshal(v)
			obj[key] = b
		}
		setInt("quantITheta", off.iTheta)
		setInt("quantIPhi", off.iPhi)
		setInt("quantIR", off.iR)
	})
}
