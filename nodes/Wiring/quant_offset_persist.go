package Wiring

// quant_offset_persist.go — the WRITE side of the quantized scalar triple (a,b,c) =
// (iTheta,iPhi,iR) as file data.
//
// A node's PERSISTED position is its EXACT scene-polar (r,θ,φ) about the scene center —
// lossless, so a dragged node reloads at exactly where it was dropped. The quantized
// scalar triple (quantITheta/quantIPhi/quantIR + steps) rides along as a self-describing
// cache of the drag-time snap cells, NOT the position source. This is the debounced
// read-modify-write mirror: RootMove calls schedule() for the dragged (and equalized)
// nodes; this persister coalesces rapid updates (a drag) into one write per node after
// motion settles, writing scenePolarR/Theta/Phi + the quant cache to
// `<root>/nodes/<id>/meta.json`, preserving every other field and dropping any leftover
// `reference` field from the removed reference-tree model.
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
// quantPersistEntry is one pending node write: the exact scene-polar position (the
// LOSSLESS source of truth — where the node actually is) plus the quantized scalar
// triple (kept as a self-describing cache/bookkeeping value, not the position source).
type quantPersistEntry struct {
	off   quantizedOffset
	scene polar // exact (r,θ,φ) of the node's continuous position about the scene center
}

type quantOffsetPersister struct {
	root     string // tree root; per-node meta.json lives at <root>/nodes/<id>/meta.json
	debounce time.Duration
	debouncedPersister[map[string]quantPersistEntry]
}

// schedule records a node's exact position (scene) plus its quantized triple and
// (re)arms the debounce timer. scene is the authoritative persisted position.
func (p *quantOffsetPersister) schedule(id string, off quantizedOffset, scene polar) {
	if p == nil || p.root == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pending == nil {
		p.pending = map[string]quantPersistEntry{}
	}
	p.pending[id] = quantPersistEntry{off: off, scene: scene}
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
	for id, e := range pend {
		if err := writeQuantOffset(p.root, id, e.off, e.scene); err != nil {
			logPersistErr("quant_offset_persist", id, err)
		}
	}
	p.recordWrite()
}

// flushPending cancels any pending debounce timer and synchronously writes whatever is
// still pending, for the clean-shutdown path (RunStdinReader) — a drag within the debounce
// window of process exit would otherwise be silently lost.
func (p *quantOffsetPersister) flushPending() {
	if p == nil {
		return
	}
	p.stop()
	p.flush()
}

// writeQuantOffset writes the node's EXACT scenePolarR/Theta/Phi (the authoritative,
// lossless position — see the package doc comment above) PLUS the quantized scalar
// triple (iTheta,iPhi,iR) as a self-describing cache of the drag-time snap cells, into
// <root>/nodes/<id>/meta.json, preserving every other field. It deletes only the
// leftover `reference` field from the removed reference-tree model. The file must
// already exist.
func writeQuantOffset(root, id string, off quantizedOffset, scene polar) error {
	if !safeTreePathComponent(id) {
		return fmt.Errorf("unsafe node id %q", id)
	}
	path := filepath.Join(root, "nodes", id, "meta.json")
	return entityReadModifyWrite(path, func(obj map[string]json.RawMessage) {
		setInt := func(key string, v int) {
			b, _ := json.Marshal(v)
			obj[key] = b
		}
		setFloat := func(key string, v float64) {
			b, _ := json.Marshal(v)
			obj[key] = b
		}
		// The EXACT scene-polar position is the authoritative, LOSSLESS record of where
		// the node actually is — the loader places the node here verbatim. Written on
		// every drag so the exact dragged position always survives reload.
		setFloat("scenePolarR", scene.R)
		setFloat("scenePolarTheta", scene.Theta)
		setFloat("scenePolarPhi", scene.Phi)
		// The quantized triple + steps are kept as a self-describing cache (the drag-time
		// snap cells), NOT the position source — the exact scenePolar above wins on load.
		setInt("quantITheta", off.iTheta)
		setInt("quantIPhi", off.iPhi)
		setInt("quantIR", off.iR)
		t, p, r := off.effectiveSteps()
		setFloat("stepTheta", t)
		setFloat("stepPhi", p)
		setFloat("stepR", r)
		delete(obj, "reference")
		// localPolars (layout_holder.go) is untouched here — entityReadModifyWrite only
		// overwrites the keys this mutation sets, so any localPolars already on disk
		// survives a position-drag write unchanged.
	})
}

// WriteLocalPolars sets the node's localPolars list (layout_holder.go LocalPolar, one
// per domain-edge neighbor, measured with this node as center) AND its measurement pole
// (the direction lh's CURRENT entries were quantized about — layout_holder.go LayoutHolder.
// Pole) in <root>/nodes/<id>/meta.json, preserving every other field — the same
// read-modify-write contract as writeQuantOffset. The pole must be persisted (not just the
// indices it produced): requantizePoleTraced reconstructs an unchanged neighbor's direction
// from its stored indices about the OLD pole, so a reload that dropped the pole would
// reconstruct against the WRONG (assumed-home) pole for any node whose pole had tilted —
// see node_move.go requantizePoleTraced's doc comment.
func WriteLocalPolars(root, id string, lps []LocalPolar, pole dir) error {
	if !safeTreePathComponent(id) {
		return fmt.Errorf("unsafe node id %q", id)
	}
	path := filepath.Join(root, "nodes", id, "meta.json")
	return entityReadModifyWrite(path, func(obj map[string]json.RawMessage) {
		type localPolarJSON struct {
			To          string  `json:"to"`
			QuantITheta int     `json:"quantITheta"`
			QuantIPhi   int     `json:"quantIPhi"`
			QuantIR     int     `json:"quantIR"`
			StepTheta   float64 `json:"stepTheta"`
			StepPhi     float64 `json:"stepPhi"`
			StepR       float64 `json:"stepR"`
		}
		out := make([]localPolarJSON, 0, len(lps))
		for _, lp := range lps {
			t, p, r := lp.effectiveSteps()
			out = append(out, localPolarJSON{
				To: lp.To, QuantITheta: lp.QuantITheta, QuantIPhi: lp.QuantIPhi, QuantIR: lp.QuantIR,
				StepTheta: t, StepPhi: p, StepR: r,
			})
		}
		b, _ := json.Marshal(out)
		obj["localPolars"] = b
		pt, _ := json.Marshal(pole.Theta)
		pp, _ := json.Marshal(pole.Phi)
		obj["localPoleTheta"] = pt
		obj["localPolePhi"] = pp
	})
}
