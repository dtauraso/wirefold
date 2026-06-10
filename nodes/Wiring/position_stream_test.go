package Wiring

import (
	"math"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
)

// position_stream_test.go — the Phase 2 deterministic verifier (golden
// position-sequence parity). It asserts that the wire's delivery goroutine emits,
// for an in-flight bead, exactly the analytic curve: each emitted position equals
// bezierPointAt(controlPoints, t) at t = elapsed/inFlightTime, with t strictly
// increasing, a final emit at t==1 immediately followed by delivery, and a ~16 ms
// emit cadence. There are NO real sleeps in the timing assertions — the FakeClock
// is advanced explicitly, so the whole stream is reproducible.
//
// "Golden" here is analytic, not a recorded file: Go's bezierPointAt IS the curve,
// so the expected sequence is recomputed in the test from the same eval the
// substrate uses. Any drift between the integrator's eval and the stream's eval
// (or a wrong t) fails this test.

// posEvents extracts the KindPosition events from a drained trace, in order.
func posEvents(events []T.Event) []T.Event {
	var out []T.Event
	for _, e := range events {
		if e.Kind == T.KindPosition {
			out = append(out, e)
		}
	}
	return out
}

// approxEq reports whether a and b are within a tight float tolerance.
func approxEq(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// TestPositionStreamGoldenSequence is the Phase 2 golden parity verifier.
//
// inFlightMs = 50, emit cadence = 16 ms ⇒ ticks at clock-elapsed 16, 32, 48 and
// the final deadline tick at 50. So the expected t sequence is
// 16/50, 32/50, 48/50, 1.0 — three regular ticks plus the clamped final tick.
// A single Advance(50ms) drives the whole walk: the goroutine wakes at each tick
// in order (WaitUntil returns as soon as Now() >= target), emitting every
// position before delivery.
func TestPositionStreamGoldenSequence(t *testing.T) {
	const inFlightMs = 50.0

	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	clk := NewFakeClock()
	pw.SetClock(clk)
	tr := T.New(64)
	pw.Trace = tr

	// A known, non-degenerate curve so x/y both vary with t.
	curve := edgeCurve{
		P0: vec3{X: 0, Y: 0, Z: 0},
		P1: vec3{X: 50, Y: 100, Z: 0},
		P2: vec3{X: 100, Y: 0, Z: 0},
	}
	bp := beadPlacement{
		InFlightMs: inFlightMs,
		P0:         curve.P0, P1: curve.P1, P2: curve.P2,
		Node: "src", Port: "out",
	}

	// Place the bead (non-blocking placement; fresh wire so it is accepted).
	if !pw.TryPlace(7, bp) {
		t.Fatal("TryPlace: expected the fresh wire to accept the bead")
	}

	// Drive the whole in-flight walk past the deadline in one Advance. The
	// delivery goroutine emits each ~16 ms tick in order, then delivers at t==1.
	clk.Advance(time.Duration(inFlightMs) * time.Millisecond)

	// Wait (with a guard, no assertion-relevant sleep) for the goroutine to finish
	// delivering — once inFlight clears, every position (incl. the final) has been
	// sent to the trace channel.
	deadline := time.Now().Add(2 * time.Second)
	for pw.InFlight() {
		if time.Now().After(deadline) {
			t.Fatal("position stream did not deliver the bead after Advance past inFlightTime")
		}
		time.Sleep(time.Millisecond)
	}

	// Drain the trace and pull the position events in order.
	tr.Close()
	positions := posEvents(tr.Events())

	// Expected tick elapsed times (ms): 16, 32, 48, then the final deadline at 50.
	expectedElapsed := []float64{16, 32, 48, 50}
	if len(positions) != len(expectedElapsed) {
		t.Fatalf("emitted %d position events, want %d (ticks 16/32/48 + final 50)\n got: %+v",
			len(positions), len(expectedElapsed), positions)
	}

	var prevT float64 = -1
	for i, e := range positions {
		elapsed := expectedElapsed[i]
		wantT := elapsed / inFlightMs
		if wantT > 1 {
			wantT = 1
		}

		// t strictly increasing across the sequence.
		if wantT <= prevT {
			t.Fatalf("position %d: t not strictly increasing (t=%g, prev=%g)", i, wantT, prevT)
		}
		prevT = wantT

		// Each emitted position == bezierPointAt(controlPoints, t) — Go's eval IS
		// the curve, so this is exact (modulo float noise).
		want := bezierPointAt(curve.P0, curve.P1, curve.P2, wantT)
		if !approxEq(e.X, want.X) || !approxEq(e.Y, want.Y) || !approxEq(e.Z, want.Z) {
			t.Fatalf("position %d (t=%g): got (%g,%g,%g), want bezierPointAt = (%g,%g,%g)",
				i, wantT, e.X, e.Y, e.Z, want.X, want.Y, want.Z)
		}

		// Source identity must be the routing key (matches the send event).
		if e.Node != "src" || e.Port != "out" {
			t.Fatalf("position %d: routing key got (%q,%q), want (\"src\",\"out\")", i, e.Node, e.Port)
		}
		// Bead value echoed.
		if e.Value != 7 {
			t.Fatalf("position %d: value got %d, want 7", i, e.Value)
		}
	}

	// The last emit is exactly t==1 (arrival), and it is the final event before
	// delivery (delivery is what cleared InFlight above).
	finalT := expectedElapsed[len(expectedElapsed)-1] / inFlightMs
	if !approxEq(finalT, 1) {
		t.Fatalf("final tick t=%g, want 1.0 (arrival emit)", finalT)
	}
	last := positions[len(positions)-1]
	wantFinal := bezierPointAt(curve.P0, curve.P1, curve.P2, 1)
	if !approxEq(last.X, wantFinal.X) || !approxEq(last.Y, wantFinal.Y) || !approxEq(last.Z, wantFinal.Z) {
		t.Fatalf("final position got (%g,%g,%g), want curve endpoint P2-ish (%g,%g,%g)",
			last.X, last.Y, last.Z, wantFinal.X, wantFinal.Y, wantFinal.Z)
	}
	// At t==1 the quadratic Bezier equals P2 exactly.
	if !approxEq(last.X, curve.P2.X) || !approxEq(last.Y, curve.P2.Y) || !approxEq(last.Z, curve.P2.Z) {
		t.Fatalf("final position (t=1) got (%g,%g,%g), want P2 (%g,%g,%g)",
			last.X, last.Y, last.Z, curve.P2.X, curve.P2.Y, curve.P2.Z)
	}

	// And delivery happened: the slot now holds the bead value.
	v, ok := pw.PollRecv()
	if !ok || v != 7 {
		t.Fatalf("after final emit, expected delivered bead 7 in slot, got (%v, ok=%v)", v, ok)
	}
}

// TestPositionStreamCadence asserts the ~16 ms emit cadence directly: with an
// inFlightMs that is a clean multiple of the 16 ms interval, every consecutive
// tick (including the final) is exactly 16 ms apart in clock-elapsed terms.
func TestPositionStreamCadence(t *testing.T) {
	const inFlightMs = 80.0 // 16 * 5

	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	clk := NewFakeClock()
	pw.SetClock(clk)
	tr := T.New(64)
	pw.Trace = tr

	curve := edgeCurve{P0: vec3{0, 0, 0}, P1: vec3{40, 80, 0}, P2: vec3{80, 0, 0}}
	bp := beadPlacement{InFlightMs: inFlightMs, P0: curve.P0, P1: curve.P1, P2: curve.P2, Node: "a", Port: "o"}

	if !pw.TryPlace(3, bp) {
		t.Fatal("TryPlace rejected on fresh wire")
	}
	clk.Advance(time.Duration(inFlightMs) * time.Millisecond)

	deadline := time.Now().Add(2 * time.Second)
	for pw.InFlight() {
		if time.Now().After(deadline) {
			t.Fatal("cadence test: bead not delivered after Advance")
		}
		time.Sleep(time.Millisecond)
	}
	tr.Close()
	positions := posEvents(tr.Events())

	// 80ms / 16ms = 5 ticks at 16,32,48,64,80; the 80 tick is the final deadline.
	if len(positions) != 5 {
		t.Fatalf("cadence: got %d positions, want 5 (16/32/48/64/80)", len(positions))
	}
	// Reconstruct each tick's elapsed from its position by inverting against the
	// analytic curve is overkill; instead assert the emitted positions match the
	// expected per-tick t with exactly 16 ms spacing.
	for i := 0; i < 5; i++ {
		elapsed := float64((i + 1) * positionEmitIntervalMs) // 16,32,48,64,80
		tt := elapsed / inFlightMs
		want := bezierPointAt(curve.P0, curve.P1, curve.P2, tt)
		got := positions[i]
		if !approxEq(got.X, want.X) || !approxEq(got.Y, want.Y) || !approxEq(got.Z, want.Z) {
			t.Fatalf("cadence tick %d (elapsed=%gms, t=%g): got (%g,%g,%g), want (%g,%g,%g)",
				i, elapsed, tt, got.X, got.Y, got.Z, want.X, want.Y, want.Z)
		}
	}
}

// TestPositionStreamHaltedNoEmit asserts the position stream is pause-aware: while
// the clock is halted, active elapsed does not advance, so NO position is emitted
// and the bead does not deliver. After Resume + Advance, the stream resumes.
func TestPositionStreamHaltedNoEmit(t *testing.T) {
	const inFlightMs = 50.0

	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	clk := NewFakeClock()
	pw.SetClock(clk)
	tr := T.New(64)
	pw.Trace = tr

	curve := edgeCurve{P0: vec3{0, 0, 0}, P1: vec3{50, 100, 0}, P2: vec3{100, 0, 0}}
	bp := beadPlacement{InFlightMs: inFlightMs, P0: curve.P0, P1: curve.P1, P2: curve.P2, Node: "s", Port: "p"}

	if !pw.TryPlace(9, bp) {
		t.Fatal("TryPlace rejected on fresh wire")
	}

	// Halt, then attempt to advance: a halted FakeClock ignores Advance, so no
	// tick deadline is reached and no position is emitted.
	clk.Halt()
	clk.Advance(10 * inFlightMs * time.Millisecond)
	// Give any (incorrectly running) goroutine a chance to emit.
	time.Sleep(20 * time.Millisecond)
	if !pw.InFlight() {
		t.Fatal("bead delivered while halted — pause must freeze the stream and delivery")
	}

	// Snapshot positions so far without closing the live trace: none should exist.
	if got := len(posEvents(tr.Events())); got != 0 {
		t.Fatalf("emitted %d positions while halted, want 0", got)
	}

	// Resume and advance past the deadline — the stream now completes.
	clk.Resume()
	clk.Advance(inFlightMs * time.Millisecond)
	deadline := time.Now().Add(2 * time.Second)
	for pw.InFlight() {
		if time.Now().After(deadline) {
			t.Fatal("bead not delivered after Resume + Advance")
		}
		time.Sleep(time.Millisecond)
	}
	tr.Close()
	if got := len(posEvents(tr.Events())); got == 0 {
		t.Fatal("no positions emitted after Resume — stream did not restart")
	}
}
