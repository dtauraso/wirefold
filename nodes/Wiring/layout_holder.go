// layout_holder.go — the per-node LOCAL POLAR list plus the pause-INDEPENDENT
// layout-update goroutine that owns it.
//
// Every domain double-link (a bidirectional edge pair A↔B) gives each endpoint
// its own LOCAL POLAR to the other, measured with ITSELF as center, in the same
// (quantITheta,quantIPhi,quantIR)×(stepTheta,stepPhi,stepR) integer-scalar form
// used for the node's absolute scene-polar position (quantized_layout.go). A node
// with N connections holds N local polars — one per neighbor, owned by that node
// alone (A's entry for B and B's entry for A are separate values).
//
// LayoutHolder is embedded into every node kind's struct (directly, or via
// gatecommon.GateNode for the two gate kinds) so builders.go can attach the
// computed list by reflection the same way it wires ports/data, and so every
// kind gets the UpdateLayout loop for free — satisfying the Node interface's
// second method without per-kind boilerplate.
//
// UpdateLayout parks on ctx.Done() the same way every node's Update loop exits
// on cancellation, but is NOT gated by the play/pause clock: it does not wait
// on WaitTick/SleepCycle, so it keeps running while beads are paused (MODEL.md:
// the play/pause gate freezes tick advance, not goroutines). For this slice it
// does no runtime mutation — it owns LocalPolars and idles; drag-time
// recomputation is a later slice.
package Wiring

import (
	"context"
	"math"
	"sync"
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

// Exported aliases of the local-polar default step constants, for external
// tooling (e.g. the one-off local-polar re-stamp migration) that needs the
// same cells RootMove's requantizeLocalPolars uses but cannot see the
// unexported constants directly.
const (
	LocalStepTheta = localStepTheta
	LocalStepPhi   = localStepPhi
	LocalStepR     = localStepR
)

// Cart2PolarOffset computes the (r,theta,phi) local-polar offset FROM one world
// point TO another, using the same polar math node_move.go's requantizeLocalPolars
// uses (cart2polar) — exported so external tooling (no access to the unexported
// vec3/polar types) can compute it without duplicating the conversion.
func Cart2PolarOffset(fromX, fromY, fromZ, toX, toY, toZ float64) (r, theta, phi float64) {
	p := cart2polar(vec3{X: toX - fromX, Y: toY - fromY, Z: toZ - fromZ})
	return p.R, p.Theta, p.Phi
}

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
// LocalPolars list (one per domain-edge neighbor) and runs the pause-independent
// layout-update goroutine. mu guards LocalPolars against concurrent access from
// a drag's requantizeLocalPolars (node_move.go, on the stdin-reader goroutine)
// and this node's own layout goroutine (currently idle, but the mutex makes
// concurrent access safe by construction rather than by convention).
type LayoutHolder struct {
	mu          sync.Mutex
	LocalPolars []LocalPolar
}

// UpdateLayout runs this node's layout-update loop until ctx is cancelled. It is
// NOT gated by the play/pause clock — it parks on ctx.Done() only (the same
// cancellation wait every node's Update loop uses to exit), so it stays live
// while the bead clock is halted.
func (lh *LayoutHolder) UpdateLayout(ctx context.Context) {
	<-ctx.Done()
}

// localPolarSteps returns the effective step constants of this node's CURRENT
// stored local polar to the given neighbor (falling back to the local-polar
// defaults if no entry exists yet), so a re-quantize preserves a neighbor's
// own step constants across drags exactly like quantizedOffset does for the
// scene triple.
func (lh *LayoutHolder) localPolarSteps(to string) (t, p, r float64) {
	lh.mu.Lock()
	defer lh.mu.Unlock()
	for _, lp := range lh.LocalPolars {
		if lp.To == to {
			return lp.effectiveSteps()
		}
	}
	return LocalPolar{}.effectiveSteps()
}

// SetLocalPolar upserts this node's local-polar entry for the given neighbor
// (updating in place if present, appending otherwise). The sole in-memory
// writer of LocalPolars outside load-time construction.
func (lh *LayoutHolder) SetLocalPolar(to string, quantITheta, quantIPhi, quantIR int, stepTheta, stepPhi, stepR float64) {
	lh.mu.Lock()
	defer lh.mu.Unlock()
	for i := range lh.LocalPolars {
		if lh.LocalPolars[i].To == to {
			lh.LocalPolars[i].QuantITheta = quantITheta
			lh.LocalPolars[i].QuantIPhi = quantIPhi
			lh.LocalPolars[i].QuantIR = quantIR
			lh.LocalPolars[i].StepTheta = stepTheta
			lh.LocalPolars[i].StepPhi = stepPhi
			lh.LocalPolars[i].StepR = stepR
			return
		}
	}
	lh.LocalPolars = append(lh.LocalPolars, LocalPolar{
		To: to, QuantITheta: quantITheta, QuantIPhi: quantIPhi, QuantIR: quantIR,
		StepTheta: stepTheta, StepPhi: stepPhi, StepR: stepR,
	})
}

// LocalPolarsSnapshot returns a defensive copy of this node's current
// LocalPolars list, safe to hand to a persister running on another goroutine.
func (lh *LayoutHolder) LocalPolarsSnapshot() []LocalPolar {
	lh.mu.Lock()
	defer lh.mu.Unlock()
	out := make([]LocalPolar, len(lh.LocalPolars))
	copy(out, lh.LocalPolars)
	return out
}
