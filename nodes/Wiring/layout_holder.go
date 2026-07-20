// layout_holder.go — the per-node LOCAL POLAR list.
//
// Every domain double-link (a bidirectional edge pair A↔B) gives each endpoint
// its own LOCAL POLAR to the other, measured with ITSELF as center, in the same
// (quantITheta,quantIPhi,quantIR)×(stepTheta,stepPhi,stepR) integer-scalar form
// used for the node's absolute scene-polar position (quantized_layout.go). A node
// with N connections holds N local polars — one per neighbor, owned by that node
// alone (A's entry for B and B's entry for A are separate values).
//
// LayoutHolder is embedded into every node kind's struct (directly, or via
// gatecommon.GateNode for the two gate kinds) so loader.go can locate it by
// reflection (the same field-lookup used for port/data injection) and load the
// computed list through LoadLocalPolars.
package Wiring

import (
	"math"
)

// Default local-polar quantization cells — SMALL and uniform across every node,
// unlike the scene-center triple's stepR=20/π-12 steps. The point of the
// double-link local-polar model is that a moved node's distance to each
// neighbor always lands on a WHOLE tick of that neighbor's own small grid;
// coarse scene-sized cells would leave most drags at iR=0/1 with no
// resolution. Used as the fallback whenever a LocalPolar has no stored
// per-neighbor step constants (LocalPolar.StepTheta/StepPhi/StepR == 0).
const (
	localStepTheta = math.Pi / 180 // 1 degree
	localStepPhi   = math.Pi / 180 // 1 degree
	localStepR     = 2.0           // world units
)

// LocalPolar is one node's local-polar offset to a neighbor it shares a domain
// edge with, measured with the OWNING node as center, in the same integer-scalar
// form as quantizedOffset (quantized_layout.go).
type LocalPolar struct {
	To string // neighbor node id

	QuantITheta int
	QuantIPhi   int
	QuantIR     int

	// Per-neighbor step constants — same "own constants, default-fallback"
	// contract as quantizedOffset.cTheta/cPhi/cR. Zero means unset (falls back
	// to the package's local-polar defaults: localStepTheta/localStepPhi/localStepR).
	StepTheta float64
	StepPhi   float64
	StepR     float64
}

// effectiveSteps mirrors quantizedOffset.effectiveSteps: this local polar's own
// step constants, falling back to the SMALL local-polar defaults (NOT the scene
// triple's coarser stepTheta/stepPhi/stepR) for any unset component.
func (lp LocalPolar) effectiveSteps() (t, p, r float64) {
	t, p, r = lp.StepTheta, lp.StepPhi, lp.StepR
	if t == 0 {
		t = localStepTheta
	}
	if p == 0 {
		p = localStepPhi
	}
	if r == 0 {
		r = localStepR
	}
	return
}

// LayoutHolder is embedded into every node kind's struct. It owns this node's
// LocalPolars list (one per domain-edge neighbor).
//
// Invariant: a LayoutHolder is written and read ONLY by its owning node's own
// goroutine. RootMove (node_move.go) routes a drag's moveMsgKindDrag to the
// DRAGGED NODE'S OWN inbox, so commitLocal -> requantizeLocalPolars runs on
// that node's own goroutine (nodeMover.handle). A node never reaches into a
// NEIGHBOR's LayoutHolder directly: it sends a moveMsgKindNeighborSetC message
// (via its own retry queue, onto the neighbor's directed neighborIn channel),
// and it is the neighbor's own run/handle goroutine that drains that message
// and calls neighborSetCRequantize -> lh.SetLocalPolar/SetPole on ITS OWN
// holder. One holder, one owning goroutine, neighbors reached only by
// message — no cross-goroutine access to guard against, so no lock is needed.
// Do not re-add one without first breaking this invariant on purpose.
type LayoutHolder struct {
	localPolars []LocalPolar
	// pole is the measurement pole (rotating_pole.go localPole result) that
	// localPolars' current QuantITheta/QuantIPhi entries were last quantized about.
	// Persisted (WriteLocalPolars) so a reload reconstructs identical world directions
	// without re-deriving from live cartesian — see requantizePoleTraced's doc comment
	// in node_move.go for why this must be carried rather than recomputed from scratch
	// against an assumed home pole.
	pole dir
}

// localPolarSteps returns the effective step constants of this node's CURRENT
// stored local polar to the given neighbor (falling back to the local-polar
// defaults if no entry exists yet), so a re-quantize preserves a neighbor's
// own step constants across drags exactly like quantizedOffset does for the
// scene triple.
func (lh *LayoutHolder) localPolarSteps(to string) (t, p, r float64) {
	for _, lp := range lh.localPolars {
		if lp.To == to {
			return lp.effectiveSteps()
		}
	}
	return LocalPolar{}.effectiveSteps()
}

// LoadLocalPolars replaces this node's entire local-polar list. Used
// exactly once, at load time (loader.go), to attach the freshly-computed list
// (computeLocalPolars) to the node's own LayoutHolder — the only initial-load
// writer, distinct from SetLocalPolar's per-neighbor upsert used by drags.
func (lh *LayoutHolder) LoadLocalPolars(lps []LocalPolar) {
	lh.localPolars = lps
}

// SetLocalPolar upserts this node's local-polar entry for the given neighbor
// (updating in place if present, appending otherwise). The sole in-memory
// writer of LocalPolars outside load-time construction.
func (lh *LayoutHolder) SetLocalPolar(to string, quantITheta, quantIPhi, quantIR int, stepTheta, stepPhi, stepR float64) {
	for i := range lh.localPolars {
		if lh.localPolars[i].To == to {
			lh.localPolars[i].QuantITheta = quantITheta
			lh.localPolars[i].QuantIPhi = quantIPhi
			lh.localPolars[i].QuantIR = quantIR
			lh.localPolars[i].StepTheta = stepTheta
			lh.localPolars[i].StepPhi = stepPhi
			lh.localPolars[i].StepR = stepR
			return
		}
	}
	lh.localPolars = append(lh.localPolars, LocalPolar{
		To: to, QuantITheta: quantITheta, QuantIPhi: quantIPhi, QuantIR: quantIR,
		StepTheta: stepTheta, StepPhi: stepPhi, StepR: stepR,
	})
}

// LocalPolarsSnapshot returns a defensive copy of this node's current
// LocalPolars list, safe to hand to a persister running on another goroutine.
func (lh *LayoutHolder) LocalPolarsSnapshot() []LocalPolar {
	out := make([]LocalPolar, len(lh.localPolars))
	copy(out, lh.localPolars)
	return out
}

// Pole returns the measurement pole this node's current LocalPolars entries were last
// quantized about (world +y, dir{0,0}, if never set — the home pole default).
func (lh *LayoutHolder) Pole() dir {
	return lh.pole
}

// SetPole records the measurement pole the CURRENT LocalPolars entries were quantized
// about, so a later requantize (or a reload) can reconstruct an unchanged neighbor's
// direction from its stored indices without re-measuring live cartesian geometry.
func (lh *LayoutHolder) SetPole(p dir) {
	lh.pole = p
}
