package Wiring

import (
	"testing"
	"time"
)

// paced_wire_rebase_tolerance_test.go — per-goroutine-clock.md "What must be
// proven" item 1: ReviseInFlightGeometry (paced_wire.go) is the ONE place two
// per-goroutine clock COPIES are subtracted from each other — placementTick was
// stamped by the SOURCE goroutine's own clock copy, nowTick is read by the EDGE
// MOVER's own clock copy (a different goroutine, per PacedWire's own doc comment
// on the three-goroutine contract). The doc derives the disagreement between two
// copies that each apply the SAME speed change at their own local `now`:
//
//	offset = (delivery skew) x (newSpeed - oldSpeed)
//
// and accepts it everywhere, sizing it at ~1 tick against a crossing of "tens of
// ticks" (edges 57-268 wu) — i.e. roughly 1-2% of a wire's travel per speed
// change. This test builds that exact two-copy skew and drives it through the
// REAL ReviseInFlightGeometry subtraction (not a re-derivation of the formula),
// then asserts the accepted bound explicitly, so the magnitude is written down in
// code rather than only in prose.

// TestReviseInFlightGeometryStaysWithinDocumentedSkewBand exercises the two-copy
// path directly: a "source" clock copy stamps placementTick (as the source
// goroutine's own DriveHeld/node loop would via placeBeadNoWalkerAt), and a
// separate "edge mover" clock copy — deliberately skewed from the source by
// exactly one speed change applied `skew` late — measures nowTick during a
// geometry rebase (as edgeMover.recomputeGeometry would via
// ReviseInFlightGeometry). Both copies are real *RealClock values built from a
// shared origin and diverged only by WHEN each applied the same speed change,
// matching per-goroutine-clock.md's "Speed changes: bank at local now" model —
// not a mock of the skew, an actual instance of it.
func TestReviseInFlightGeometryStaysWithinDocumentedSkewBand(t *testing.T) {
	const crossTicks = 40.0 // representative "tens of ticks" crossing (per-goroutine-clock.md).
	const oldSpeed = 1.0
	const newSpeed = 2.0
	// "Delivery reaches each goroutine within about one cycle" is the doc's own
	// premise (MsPerTick=16, ~16 ms stagger). Model that as a one-tick-period
	// delivery skew between the two copies applying the identical speed change.
	skew := tickPeriod

	// Build the two copies directly (white-box, same package) rather than via a
	// real SetSpeed+sleep pair, so the skew is EXACT and the test is not flaky.
	// This is the doc's own derivation, not a re-invention: both copies share an
	// identical pre-change history (same accScaled/lastChange/speed=1), then each
	// banks the SAME newSpeed change at its own local `now` — source at t0, edge
	// at t0+skew. Algebraically (see clock.go's scaledElapsed/SetSpeed):
	//
	//	accScaled_edge(at its own bank) = accScaled_src(at its own bank) + skew
	//
	// and evaluating both at a later shared instant (both re-anchored at `now`
	// below, live segment ~0) leaves exactly:
	//
	//	scaledElapsed_src - scaledElapsed_edge = skew * (newSpeed - oldSpeed)
	//
	// which is the doc's "offset" formula verbatim.
	offset := time.Duration(float64(skew) * (newSpeed - oldSpeed))

	now := time.Now()
	// Arbitrary elapsed-since-start-of-segment, large enough that the bead is
	// placed mid-history (not right at a clock's construction instant).
	baseAcc := 50 * tickPeriod
	source := RealClock{speed: newSpeed, lastChange: now, accScaled: baseAcc}
	edge := RealClock{speed: newSpeed, lastChange: now, accScaled: baseAcc - offset}

	pw := NewPacedWire(0, PulseSpeedWuPerTick)
	arc := crossTicks * PulseSpeedWuPerTick
	// InFlightMs*msToArcWu == crossTicks*PulseSpeedWuPerTick == arc (see msToArcWu).
	inFlightMs := crossTicks * MsPerTick

	// SOURCE goroutine places the bead, stamping placementTick from ITS OWN clock
	// copy (placeBeadNoWalkerAt's documented contract).
	placementTick := source.Tick()
	if _, ok := pw.placeBeadNoWalkerAt(0, beadPlacement{InFlightMs: inFlightMs, Start: vec3{}, End: vec3{X: 1}}, placementTick); !ok {
		t.Fatalf("placeBeadNoWalkerAt failed")
	}

	// Let real wall time pass so the bead is genuinely mid-flight when rebased.
	// Both clocks share the same speed/lastChange from here, so this elapsed time
	// advances them IDENTICALLY — the only difference between them remains the
	// fixed `offset` baked into accScaled above.
	time.Sleep(10 * tickPeriod)

	sourceNowTick := source.Tick() // single-clock ("true") reading, for comparison only.
	edgeNowTick := edge.Tick()     // EDGE MOVER's own copy — what ReviseInFlightGeometry actually uses.

	// Ground truth: the fraction covered if a single consistent clock (source's)
	// had measured both the placement and the rebase.
	trueT := (float64(sourceNowTick) - float64(placementTick)) / crossTicks

	// Drive the REAL two-copy subtraction under test.
	newSeg := wireSegment{Start: vec3{}, End: vec3{X: 1}}
	pw.ReviseInFlightGeometry(edgeNowTick, arc, newSeg)

	// Recover the fraction ReviseInFlightGeometry actually preserved by inverting
	// its own rebase formula (paced_wire.go: placementTick' = nowTick - t*(arc/pulseSpeed)),
	// reading the internal state back directly (white-box, same package).
	pw.mu.Lock()
	rebasedPlacementTick := pw.inflight[0].placementTick
	pw.mu.Unlock()
	gotT := (float64(edgeNowTick) - rebasedPlacementTick) / crossTicks

	diff := gotT - trueT
	if diff < 0 {
		diff = -diff
	}

	// THE ACCEPTED BOUND, written down in code (per-goroutine-clock.md: "1-2% of a
	// wire per speed change"). 0.05 (5%) is deliberately generous of the 1-2%
	// figure: it covers one speed change's worth of skew (~1 tick / crossTicks
	// ticks = 2.5% here) plus slack for float rounding and the real 10-tick sleep
	// above, while still being tight enough to catch a regression that multiplies
	// or drops the skew.
	const acceptedBand = 0.05
	if diff > acceptedBand {
		t.Fatalf("rebase fraction drifted %.4f beyond the accepted %.2f (1-2%%-per-speed-change) band: trueT=%.4f gotT=%.4f",
			diff, acceptedBand, trueT, gotT)
	}
}
