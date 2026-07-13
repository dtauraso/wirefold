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
// UpdateLayout is the SAME loop shape as Update (a `for { select { ... } }`
// parked on ctx.Done()) but is NOT gated by the play/pause clock: it does not
// wait on WaitTick/SleepCycle, so it keeps running while beads are paused
// (MODEL.md: the play/pause gate freezes tick advance, not goroutines). For
// this slice it does no runtime mutation — it owns LocalPolars and idles;
// drag-time recomputation is a later slice.
package Wiring

import "context"

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
	// to the package's global stepTheta/stepPhi/stepR defaults).
	StepTheta float64
	StepPhi   float64
	StepR     float64
}

// effectiveSteps mirrors quantizedOffset.effectiveSteps: this local polar's own
// step constants, falling back to the global defaults for any unset component.
func (lp LocalPolar) effectiveSteps() (t, p, r float64) {
	t, p, r = lp.StepTheta, lp.StepPhi, lp.StepR
	if t == 0 {
		t = stepTheta
	}
	if p == 0 {
		p = stepPhi
	}
	if r == 0 {
		r = stepR
	}
	return
}

// LayoutHolder is embedded into every node kind's struct. It owns this node's
// LocalPolars list (one per domain-edge neighbor) and runs the pause-independent
// layout-update goroutine.
type LayoutHolder struct {
	LocalPolars []LocalPolar
}

// UpdateLayout runs this node's layout-update loop until ctx is cancelled. It is
// NOT gated by the play/pause clock — it parks on ctx.Done() only, so it stays
// live while the bead clock is halted. Same loop shape as a node's Update (a
// `for { select { case <-ctx.Done(): return } }`); for this slice it performs no
// runtime mutation of LocalPolars — later drag-time work fills the loop body in.
func (lh *LayoutHolder) UpdateLayout(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		<-ctx.Done()
		return
	}
}
