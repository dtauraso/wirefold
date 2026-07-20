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
// gatecommon.GateNode for the two gate kinds) so loader.go can locate it by
// reflection (the same field-lookup used for port/data injection) and load the
// computed list through LoadLocalPolars, and so every kind gets the
// UpdateLayout loop for free — satisfying the Node interface's second method
// without per-kind boilerplate.
//
// UpdateLayout parks on ctx.Done() the same way every node's Update loop exits
// on cancellation. It does not pace on SleepCycle — it owns LocalPolars and idles
// until cancellation. For this slice it
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
// layout-update goroutine (UpdateLayout below is currently a no-op that only
// waits on ctx.Done()).
//
// mu does NOT guard against the stdin-reader goroutine: RootMove (node_move.go)
// routes a drag's moveMsgKindDrag to the DRAGGED NODE'S OWN
// inbox, so commitLocal -> requantizeLocalPolars runs on that node's own
// goroutine (nodeMover.handle), never on the stdin reader.
//
// REFUTED (a third time — this comment previously named the wrong goroutine
// pair twice): a prior version of this comment claimed node X's own goroutine
// calls SetLocalPolar/LocalPolarsSnapshot/Pole DIRECTLY on a NEIGHBOR node M's
// LayoutHolder while M's own goroutine concurrently mutates the same fields.
// That is false. requantizeLocalPolars (quantized_move.go) only ever looks up
// md.layoutHolders[nodeID] — X itself — and never md.layoutHolders[m] for a
// neighbor m; X reaches M exclusively by sending it a moveMsgKindNeighborSetC
// message (via X's own retry queue, onto M's own directed neighborIn channel), and it is M's own run/handle
// goroutine (node_mover.go) that drains that message and calls
// neighborSetCRequantize -> lh.SetLocalPolar/SetPole on M's OWN holder. Every
// LayoutHolder in md.layoutHolders is therefore written and read ONLY by its
// owning node's single per-node goroutine (channel-serialized, one at a time,
// same pattern as nodeMover.quantOffset) — there is no cross-goroutine writer
// of a given holder to guard against.
//
// NOT CHECKED BY A TEST, deliberately. A test was written to drive the claimed
// X-writes-M contention concurrently through RootMove and could NOT be made to
// fail with mu removed (10/10 clean runs under -race) — because the contention
// does not exist. Every other mutex in this package now carries a test that
// provably goes red when its guard is removed; this one cannot, so shipping one
// would have meant shipping a test that can never fail, which is the exact
// failure mode that discipline exists to prevent. The absence of a test here is
// the honest signal.
//
// So mu is currently UNCONTENDED. It is retained as cheap insurance against a
// future direct md.layoutHolders[m] call from another node's goroutine, not
// because it guards a present race. Do not cite it as evidence that
// cross-goroutine holder access is expected — it is not.
type LayoutHolder struct {
	mu          sync.Mutex
	localPolars []LocalPolar
	// pole is the measurement pole (rotating_pole.go localPole result) that
	// localPolars' current QuantITheta/QuantIPhi entries were last quantized about.
	// Persisted (WriteLocalPolars) so a reload reconstructs identical world directions
	// without re-deriving from live cartesian — see requantizePoleTraced's doc comment
	// in node_move.go for why this must be carried rather than recomputed from scratch
	// against an assumed home pole.
	pole dir
}

// UpdateLayout runs this node's layout-update loop until ctx is cancelled. It
// parks on ctx.Done() only (the same cancellation wait every node's Update loop
// uses to exit).
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
	for _, lp := range lh.localPolars {
		if lp.To == to {
			return lp.effectiveSteps()
		}
	}
	return LocalPolar{}.effectiveSteps()
}

// LoadLocalPolars replaces this node's entire local-polar list under lock. Used
// exactly once, at load time (loader.go), to attach the freshly-computed list
// (computeLocalPolars) to the node's own LayoutHolder — the only initial-load
// writer, distinct from SetLocalPolar's per-neighbor upsert used by drags.
func (lh *LayoutHolder) LoadLocalPolars(lps []LocalPolar) {
	lh.mu.Lock()
	defer lh.mu.Unlock()
	lh.localPolars = lps
}

// SetLocalPolar upserts this node's local-polar entry for the given neighbor
// (updating in place if present, appending otherwise). The sole in-memory
// writer of LocalPolars outside load-time construction.
func (lh *LayoutHolder) SetLocalPolar(to string, quantITheta, quantIPhi, quantIR int, stepTheta, stepPhi, stepR float64) {
	lh.mu.Lock()
	defer lh.mu.Unlock()
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
	lh.mu.Lock()
	defer lh.mu.Unlock()
	out := make([]LocalPolar, len(lh.localPolars))
	copy(out, lh.localPolars)
	return out
}

// Pole returns the measurement pole this node's current LocalPolars entries were last
// quantized about (world +y, dir{0,0}, if never set — the home pole default).
func (lh *LayoutHolder) Pole() dir {
	lh.mu.Lock()
	defer lh.mu.Unlock()
	return lh.pole
}

// SetPole records the measurement pole the CURRENT LocalPolars entries were quantized
// about, so a later requantize (or a reload) can reconstruct an unchanged neighbor's
// direction from its stored indices without re-measuring live cartesian geometry.
func (lh *LayoutHolder) SetPole(p dir) {
	lh.mu.Lock()
	defer lh.mu.Unlock()
	lh.pole = p
}
