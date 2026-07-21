package Wiring

// quant_offset_persist.go — the WRITE side of the quantized scalar triple (a,b,c) =
// (iTheta,iPhi,iR) as file data.
//
// A node's PERSISTED position is its EXACT scene-polar (r,θ,φ) about the scene center —
// lossless, so a dragged node reloads at exactly where it was dropped. The quantized
// scalar triple (quantITheta/quantIPhi/quantIR + steps) rides along as a self-describing
// cache of the drag-time snap cells, NOT the position source. commitNodeMoveLocal calls
// schedule() for the dragged node, writing scenePolarR/Theta/Phi + the quant cache to
// `<root>/nodes/<id>/position.json` — one-file-per-writer: this file has exactly one writer
// per node id (writeQuantOffset), so each write is a fresh whole-file marshal, no
// read-modify-write, no entityFileMu (deleted). Static node identity (id/type/r/gate) stays
// in meta.json, which this persister never touches. WriteLocalPolars below is the OTHER
// former meta.json writer; it now owns its own file (local-polars.json) for the same reason.
//
// Go owns persistence (MODEL.md): fire-and-forget, SYNCHRONOUS — schedule() writes
// immediately, inline on the caller's own goroutine (see scene_persist.go's header comment
// for why the prior debounce was removed) — logs on error, never blocks the gesture. Only
// the directory-tree form has a per-node position.json to write; root == "" (monolithic
// topology.json) is a no-op.
//
// LEGACY FALLBACK: an existing pre-split topology has these fields inline in meta.json
// instead of a separate position.json/local-polars.json — loader_tree.go's loadTree reads
// meta.json first (still required — it owns id/type/r/gate) and then overlays position.json
// / local-polars.json when present, so an old topology still loads unchanged and the next
// drag/move writes forward into the new files without ever migrating or deleting meta.json.

import (
	"fmt"
	"path/filepath"
)

// quantOffsetPersister writes a node's scalar-triple change straight to its
// position.json as it happens. Owned by MoveDispatch (armed by EnableEditPersist).
// root == "" disables it (monolithic form / unarmed tests).
//
// UNLIKE this package's other four persisters, this one has MULTIPLE writers: every node
// has its OWN mover goroutine, and commitNodeMoveLocal runs schedule() on that node's own
// goroutine — so two different nodes' drags can call schedule() on this SAME
// quantOffsetPersister struct concurrently. That is safe without a lock because they write
// to DIFFERENT files (position.json is keyed by node id, so no two calls ever race the same
// os.WriteFile/Rename) and this struct holds no other shared mutable state.
type quantOffsetPersister struct {
	root string // tree root; per-node position.json lives at <root>/nodes/<id>/position.json
}

// schedule writes the given node's exact position (scene) plus its quantized triple to
// position.json synchronously. scene is the authoritative persisted position.
func (p *quantOffsetPersister) schedule(id string, off quantizedOffset, scene polar) {
	if p == nil || p.root == "" {
		return
	}
	if err := writeQuantOffset(p.root, id, off, scene); err != nil {
		logPersistErr("quant_offset_persist", id, err)
		return
	}
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
