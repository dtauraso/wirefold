// layout_holder.go — the per-node LOCAL POLAR list plus the pause-INDEPENDENT
// layout-update goroutine that owns it.
//
// Every domain double-link (a bidirectional edge pair A↔B) gives each endpoint
// its own LOCAL POLAR to the other, measured with ITSELF as center: an EXACT unit
// DIRECTION vector (Dir) plus a quantized RADIUS (quantIR×stepR), the latter in the
// same integer-scalar form used for the node's absolute scene-polar position
// (quantized_layout.go). The direction is stored faithfully as a unit vec3 rather
// than a quantized (θ,φ) pair about the fixed +y pole — that decomposition blew up
// near the pole (one φ-cell spans r·sinθ·stepφ → 0 as θ→0), so a fixed world nudge
// crossed unbounded φ-cells. Storing the exact direction makes that bug class
// unrepresentable; there is no live consumer that reconstructs a world position
// from this direction today (the lock cascade is purely distance/QuantIR-driven),
// so there is nothing to "fix" about precision loss — only about the pole blow-up.
// A node with N connections holds N local polars — one per neighbor, owned by that
// node alone (A's entry for B and B's entry for A are separate values).
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
	"sync"
)

// Default local-polar quantization cell for the radius — SMALL and uniform across
// every node, unlike the scene-center triple's stepR. The point of the
// double-link local-polar model is that a moved node's distance to each
// neighbor always lands on a WHOLE tick of that neighbor's own small grid;
// coarse scene-sized cells would leave most drags at iR=0/1 with no
// resolution. Used as the fallback whenever a LocalPolar has no stored
// per-neighbor step constant (LocalPolar.StepR == 0). Direction has no step —
// it is stored as an exact unit vector, not quantized.
const (
	localStepR = 2.0 // world units
)

// LocalPolar is one node's local-polar offset to a neighbor it shares a domain
// edge with, measured with the OWNING node as center: an EXACT unit direction
// vector plus a quantized radius (same integer-scalar radius form as
// quantizedOffset, quantized_layout.go).
type LocalPolar struct {
	To string // neighbor node id

	// Role names this neighbor's part in the decentralized cascade rule
	// (node_move.go): "source" is the ONE neighbor this node measures its
	// reference length L against; "follower" is a neighbor this node
	// repositions (via an Equalize message) to L. "" (absent) is neither —
	// authored in the spec (meta.json localPolars[].role), never computed.
	Role string

	// Dir is the EXACT unit direction of (neighbor − owner), stored faithfully
	// rather than decomposed into a quantized (θ,φ) pair about the fixed +y pole
	// (that decomposition is what blew up near the pole). Zero vector when
	// unmeasurable (centerless neighbor at load time).
	Dir vec3

	QuantIR int

	// Per-neighbor step constant for the radius only — same "own constant,
	// default-fallback" contract as quantizedOffset.cR. Zero means unset (falls
	// back to the package's local-polar default: localStepR).
	StepR float64
}

// effectiveSteps mirrors quantizedOffset.effectiveSteps: this local polar's own
// radius step constant, falling back to the SMALL local-polar default (NOT the
// scene triple's coarser stepR) when unset. Direction has no step to report —
// it is stored as an exact unit vector.
func (lp LocalPolar) effectiveSteps() (r float64) {
	r = lp.StepR
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
	localPolars []LocalPolar
}

// UpdateLayout runs this node's layout-update loop until ctx is cancelled. It
// parks on ctx.Done() only (the same cancellation wait every node's Update loop
// uses to exit).
func (lh *LayoutHolder) UpdateLayout(ctx context.Context) {
	<-ctx.Done()
}

// localPolarSteps returns the effective radius step constant of this node's
// CURRENT stored local polar to the given neighbor (falling back to the
// local-polar default if no entry exists yet), so a re-quantize preserves a
// neighbor's own step constant across drags exactly like quantizedOffset does
// for the scene triple. Direction has no step (stored as an exact unit vector).
func (lh *LayoutHolder) localPolarSteps(to string) (r float64) {
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
// writer of LocalPolars outside load-time construction. dir is the EXACT unit
// direction to the neighbor (zero vector when unmeasurable).
func (lh *LayoutHolder) SetLocalPolar(to string, dir vec3, quantIR int, stepR float64) {
	lh.mu.Lock()
	defer lh.mu.Unlock()
	for i := range lh.localPolars {
		if lh.localPolars[i].To == to {
			lh.localPolars[i].Dir = dir
			lh.localPolars[i].QuantIR = quantIR
			lh.localPolars[i].StepR = stepR
			return
		}
	}
	lh.localPolars = append(lh.localPolars, LocalPolar{
		To: to, Dir: dir, QuantIR: quantIR, StepR: stepR,
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
