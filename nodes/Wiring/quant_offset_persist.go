package Wiring

// quant_offset_persist.go — the WRITE side of the quantized scalar triple (a,b,c) =
// (iTheta,iPhi,iR) as file data, plus the owned reference.
//
// Under the plain-polar model (quantized_layout.go measureScalars/deriveCenters,
// node_move.go snapToReference/remeasureTriples) a node's PERSISTED position is its
// integer scalar triple about its reference — NOT a cartesian or scene-polar center.
// This is the debounced read-modify-write mirror: RootMove / remeasureTriples call
// schedule() for the dragged node and every direct child whose triple changed; this
// persister coalesces rapid updates (a drag) into one write per node after motion
// settles, and writes quantITheta/quantIPhi/quantIR + reference to
// `<root>/nodes/<id>/meta.json`, preserving every other field and DELETING the legacy
// scenePolarR/Theta/Phi fields (scalars are now the sole persisted position source).
//
// Go owns persistence (MODEL.md): fire-and-forget, runs on the debounce timer's own
// goroutine, logs on error, never blocks the gesture. Only the directory-tree form has
// a per-node meta.json to write; root == "" (monolithic topology.json) is a no-op.

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"
)

// quantOffsetPersister coalesces rapid per-node scalar-triple changes (a drag, plus its
// direct children's re-measured triples) into a debounced read-modify-write of each
// node's meta.json quantITheta/quantIPhi/quantIR + reference. Owned by MoveDispatch
// (armed by EnableEditPersist). root == "" disables it (monolithic form / unarmed tests).
type quantOffsetPersister struct {
	root     string // tree root; per-node meta.json lives at <root>/nodes/<id>/meta.json
	debounce time.Duration
	debouncedPersister[map[string]quantizedOffset]
}

// schedule records the latest scalar triple + reference for a node and (re)arms the
// debounce timer. off.parent is overwritten with ref so the persisted `reference` field
// always matches the caller's authoritative md.references entry even if off was built
// before a reference change.
func (p *quantOffsetPersister) schedule(id string, off quantizedOffset, ref string) {
	if p == nil || p.root == "" {
		return
	}
	off.parent = ref
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

// flush writes every pending node's scalar triple + reference to its meta.json
// (read-modify-write, preserving other fields) and clears the pending set.
// Fire-and-forget: errors are logged, not returned.
func (p *quantOffsetPersister) flush() {
	pend, has := p.take()
	if !has || len(pend) == 0 {
		return
	}
	for id, off := range pend {
		if err := writeQuantOffset(p.root, id, off); err != nil {
			logPersistErr("quant_offset_persist", id, err)
		}
	}
	p.recordWrite()
}

// writeQuantOffset sets the node's quantized scalar triple (iTheta,iPhi,iR) and its
// reference in <root>/nodes/<id>/meta.json, preserving every other field and DELETING
// the legacy scenePolarR/Theta/Phi fields (the scalar triple is now the sole persisted
// position source — see the package doc comment above). The file must already exist.
func writeQuantOffset(root, id string, off quantizedOffset) error {
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
		if off.parent == "" {
			delete(obj, "reference")
		} else {
			b, _ := json.Marshal(off.parent)
			obj["reference"] = b
		}
		// Scene polar is no longer a stored source of truth — drop any legacy fields.
		delete(obj, "scenePolarR")
		delete(obj, "scenePolarTheta")
		delete(obj, "scenePolarPhi")
	})
}
