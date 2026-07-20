package Wiring

// quant_offset_persist.go — the WRITE side of the quantized scalar triple (a,b,c) =
// (iTheta,iPhi,iR) as file data.
//
// A node's PERSISTED position is its EXACT scene-polar (r,θ,φ) about the scene center —
// lossless, so a dragged node reloads at exactly where it was dropped. The quantized
// scalar triple (quantITheta/quantIPhi/quantIR + steps) rides along as a self-describing
// cache of the drag-time snap cells, NOT the position source. This is the debounced
// mirror: RootMove calls schedule() for the dragged (and equalized) nodes; this persister
// coalesces rapid updates (a drag) into one write per node after motion settles, writing
// scenePolarR/Theta/Phi + the quant cache to `<root>/nodes/<id>/position.json` — one-file-
// per-writer (docs/planning/visual-editor/one-file-per-goroutine.md): this file has exactly
// one writer (writeQuantOffset), so each write is a fresh whole-file marshal, no
// read-modify-write, no entityFileMu (deleted). Static node identity (id/type/r/gate) stays
// in meta.json, which this persister never touches. WriteLocalPolars below is the OTHER
// former meta.json writer; it now owns its own file (local-polars.json) for the same reason.
//
// Go owns persistence (MODEL.md): fire-and-forget, runs on the debounce timer's own
// goroutine, logs on error, never blocks the gesture. Only the directory-tree form has
// a per-node position.json to write; root == "" (monolithic topology.json) is a no-op.
//
// LEGACY FALLBACK: an existing pre-split topology has these fields inline in meta.json
// instead of a separate position.json/local-polars.json — loader_tree.go's loadTree reads
// meta.json first (still required — it owns id/type/r/gate) and then overlays position.json
// / local-polars.json when present, so an old topology still loads unchanged and the next
// drag/move writes forward into the new files without ever migrating or deleting meta.json.

import (
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

// positionFileJSON is the shape of nodes/<id>/position.json — the node's exact scene-polar
// position plus its quantized-scalar-triple cache. Mirrors the equivalent fields of
// loader_tree.go's jsonMeta (the legacy, still-read fallback shape).
type positionFileJSON struct {
	ScenePolarR     float64 `json:"scenePolarR"`
	ScenePolarTheta float64 `json:"scenePolarTheta"`
	ScenePolarPhi   float64 `json:"scenePolarPhi"`
	QuantITheta     int     `json:"quantITheta"`
	QuantIPhi       int     `json:"quantIPhi"`
	QuantIR         int     `json:"quantIR"`
	StepTheta       float64 `json:"stepTheta"`
	StepPhi         float64 `json:"stepPhi"`
	StepR           float64 `json:"stepR"`
}

// positionFilePath is <root>/nodes/<id>/position.json.
func positionFilePath(root, id string) string {
	return filepath.Join(root, "nodes", id, "position.json")
}

// writeQuantOffset writes the node's EXACT scenePolarR/Theta/Phi (the authoritative,
// lossless position — see the package doc comment above) PLUS the quantized scalar triple
// (iTheta,iPhi,iR) as a self-describing cache of the drag-time snap cells, as the WHOLE
// content of <root>/nodes/<id>/position.json — the sole writer of that file, so each write
// is a fresh marshal (no read-modify-write, and no leftover `reference` field to drop: that
// was a meta.json-only artifact of the removed reference-tree model).
func writeQuantOffset(root, id string, off quantizedOffset, scene polar) error {
	if !safeTreePathComponent(id) {
		return fmt.Errorf("unsafe node id %q", id)
	}
	t, p, r := off.effectiveSteps()
	return writeJSONAtomic(positionFilePath(root, id), positionFileJSON{
		ScenePolarR: scene.R, ScenePolarTheta: scene.Theta, ScenePolarPhi: scene.Phi,
		QuantITheta: off.iTheta, QuantIPhi: off.iPhi, QuantIR: off.iR,
		StepTheta: t, StepPhi: p, StepR: r,
	})
}

// localPolarJSON mirrors one entry of a node's persisted localPolars list (also the shape
// of loader.go's specLocalPolar / loader_tree.go's legacy jsonMeta.LocalPolars entries).
type localPolarJSON struct {
	To          string  `json:"to"`
	QuantITheta int     `json:"quantITheta"`
	QuantIPhi   int     `json:"quantIPhi"`
	QuantIR     int     `json:"quantIR"`
	StepTheta   float64 `json:"stepTheta"`
	StepPhi     float64 `json:"stepPhi"`
	StepR       float64 `json:"stepR"`
}

// localPolarsFileJSON is the shape of nodes/<id>/local-polars.json.
type localPolarsFileJSON struct {
	LocalPolars    []localPolarJSON `json:"localPolars"`
	LocalPoleTheta float64          `json:"localPoleTheta"`
	LocalPolePhi   float64          `json:"localPolePhi"`
}

// localPolarsFilePath is <root>/nodes/<id>/local-polars.json.
func localPolarsFilePath(root, id string) string {
	return filepath.Join(root, "nodes", id, "local-polars.json")
}

// WriteLocalPolars sets the node's localPolars list (layout_holder.go LocalPolar, one per
// domain-edge neighbor, measured with this node as center) AND its measurement pole (the
// direction lh's CURRENT entries were quantized about — layout_holder.go LayoutHolder.Pole)
// as the WHOLE content of <root>/nodes/<id>/local-polars.json — the sole writer of that
// file, so each write is a fresh marshal (no read-modify-write). The pole must be persisted
// (not just the indices it produced): requantizePoleTraced reconstructs an unchanged
// neighbor's direction from its stored indices about the OLD pole, so a reload that dropped
// the pole would reconstruct against the WRONG (assumed-home) pole for any node whose pole
// had tilted — see node_move.go requantizePoleTraced's doc comment.
func WriteLocalPolars(root, id string, lps []LocalPolar, pole dir) error {
	if !safeTreePathComponent(id) {
		return fmt.Errorf("unsafe node id %q", id)
	}
	out := make([]localPolarJSON, 0, len(lps))
	for _, lp := range lps {
		t, p, r := lp.effectiveSteps()
		out = append(out, localPolarJSON{
			To: lp.To, QuantITheta: lp.QuantITheta, QuantIPhi: lp.QuantIPhi, QuantIR: lp.QuantIR,
			StepTheta: t, StepPhi: p, StepR: r,
		})
	}
	return writeJSONAtomic(localPolarsFilePath(root, id), localPolarsFileJSON{
		LocalPolars: out, LocalPoleTheta: pole.Theta, LocalPolePhi: pole.Phi,
	})
}
