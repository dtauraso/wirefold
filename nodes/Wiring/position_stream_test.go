package Wiring

import (
	"math"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
)

// position_stream_test.go — the Phase 2 deterministic verifier (golden
// position-sequence parity). It asserts that the wire's delivery goroutine emits,
// for an in-flight bead, exactly the analytic straight-segment position:
// each emitted position equals lerp(Start, End, t) at t = tick/ticksToCross,
// with t strictly increasing, a final emit at t==1 immediately followed by
// delivery, and one emit per tick (the tick IS the animation clock). There are
// NO real sleeps in the timing assertions — the FakeClock is advanced explicitly,
// so the whole stream is reproducible. These wires use pulseSpeed =
// PulseSpeedWuPerMs, so ticksToCross == inFlightMs (one tick per old ms-unit).
//
// "Golden" here is analytic, not a recorded file: Go's lerp IS the position eval,
// so the expected sequence is recomputed in the test from the same formula the
// Go uses. Any drift between the formula and the stream fails this test.

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
// ticksToCross = 50, one emit per tick ⇒ positions at ticks 1,2,…,50, with the
// tick-50 emit clamped to t==1 (the delivery tick). A single AdvanceTicks(50)
// drives the whole walk: the goroutine wakes at each tick in order (WaitTick
// returns as soon as Tick() >= target), emitting every position before delivery.
func TestPositionStreamGoldenSequence(t *testing.T) {
	const inFlightMs = 50.0

	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	clk := NewFakeClock()
	pw.SetClock(clk)
	tr := T.New(64)
	pw.Trace = tr

	// A known, non-degenerate segment so x/y both vary with t.
	seg := wireSegment{
		Start: vec3{X: 0, Y: 0, Z: 0},
		End:   vec3{X: 100, Y: 50, Z: 0},
	}
	bp := beadPlacement{
		InFlightMs: inFlightMs,
		Start:      seg.Start, End: seg.End,
		Node: "src", Port: "out",
	}

	// Place the bead (non-blocking placement; fresh wire so it is accepted).
	if !placeAndDrive(pw, 7, bp) {
		t.Fatal("placeAndDrive: expected the fresh wire to accept the bead")
	}

	// Drive the whole in-flight walk past the deadline in one advance. The
	// delivery goroutine emits each tick's position in order, then delivers at t==1.
	clk.AdvanceTicks(int64(inFlightMs))

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

	// One emit per tick: positions at ticks 1..inFlightMs (== ticksToCross).
	wantN := int(inFlightMs)
	if len(positions) != wantN {
		t.Fatalf("emitted %d position events, want %d (one per tick 1..%d)\n got: %+v",
			len(positions), wantN, wantN, positions)
	}

	var prevT float64 = -1
	for i, e := range positions {
		tick := float64(i + 1)
		wantT := tick / inFlightMs
		if wantT > 1 {
			wantT = 1
		}

		// t strictly increasing across the sequence.
		if wantT <= prevT {
			t.Fatalf("position %d: t not strictly increasing (t=%g, prev=%g)", i, wantT, prevT)
		}
		prevT = wantT

		// Each emitted position == lerp(Start, End, t) — Go's eval IS the segment,
		// so this is exact (modulo float noise).
		want := lerp(seg.Start, seg.End, wantT)
		if !approxEq(e.X, want.X) || !approxEq(e.Y, want.Y) || !approxEq(e.Z, want.Z) {
			t.Fatalf("position %d (t=%g): got (%g,%g,%g), want lerp = (%g,%g,%g)",
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
	finalT := float64(len(positions)) / inFlightMs
	if !approxEq(finalT, 1) {
		t.Fatalf("final tick t=%g, want 1.0 (arrival emit)", finalT)
	}
	last := positions[len(positions)-1]
	wantFinal := lerp(seg.Start, seg.End, 1)
	if !approxEq(last.X, wantFinal.X) || !approxEq(last.Y, wantFinal.Y) || !approxEq(last.Z, wantFinal.Z) {
		t.Fatalf("final position got (%g,%g,%g), want segment endpoint End (%g,%g,%g)",
			last.X, last.Y, last.Z, wantFinal.X, wantFinal.Y, wantFinal.Z)
	}
	// At t==1 lerp equals End exactly.
	if !approxEq(last.X, seg.End.X) || !approxEq(last.Y, seg.End.Y) || !approxEq(last.Z, seg.End.Z) {
		t.Fatalf("final position (t=1) got (%g,%g,%g), want End (%g,%g,%g)",
			last.X, last.Y, last.Z, seg.End.X, seg.End.Y, seg.End.Z)
	}

	// And delivery happened: the slot now holds the bead value.
	v, ok := pw.PollRecv()
	if !ok || v != 7 {
		t.Fatalf("after final emit, expected delivered bead 7 in slot, got (%v, ok=%v)", v, ok)
	}
}

// TestPositionStreamCadence asserts the per-tick emit cadence directly: every
// consecutive emit (including the final) is exactly one tick apart, so there are
// ticksToCross emits with t = tick/ticksToCross.
func TestPositionStreamCadence(t *testing.T) {
	const inFlightMs = 80.0

	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	clk := NewFakeClock()
	pw.SetClock(clk)
	tr := T.New(64)
	pw.Trace = tr

	seg := wireSegment{Start: vec3{0, 0, 0}, End: vec3{80, 40, 0}}
	bp := beadPlacement{InFlightMs: inFlightMs, Start: seg.Start, End: seg.End, Node: "a", Port: "o"}

	if !placeAndDrive(pw, 3, bp) {
		t.Fatal("placeAndDrive rejected on fresh wire")
	}
	clk.AdvanceTicks(int64(inFlightMs))

	deadline := time.Now().Add(2 * time.Second)
	for pw.InFlight() {
		if time.Now().After(deadline) {
			t.Fatal("cadence test: bead not delivered after Advance")
		}
		time.Sleep(time.Millisecond)
	}
	tr.Close()
	positions := posEvents(tr.Events())

	// One emit per tick: ticksToCross == inFlightMs emits at ticks 1..80.
	wantN := int(inFlightMs)
	if len(positions) != wantN {
		t.Fatalf("cadence: got %d positions, want %d (one per tick)", len(positions), wantN)
	}
	// Each emit is exactly one tick apart: position i is at tick i+1, t=(i+1)/inFlightMs.
	for i := 0; i < wantN; i++ {
		tt := float64(i+1) / inFlightMs
		want := lerp(seg.Start, seg.End, tt)
		got := positions[i]
		if !approxEq(got.X, want.X) || !approxEq(got.Y, want.Y) || !approxEq(got.Z, want.Z) {
			t.Fatalf("cadence tick %d (t=%g): got (%g,%g,%g), want (%g,%g,%g)",
				i, tt, got.X, got.Y, got.Z, want.X, want.Y, want.Z)
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

	seg := wireSegment{Start: vec3{0, 0, 0}, End: vec3{100, 50, 0}}
	bp := beadPlacement{InFlightMs: inFlightMs, Start: seg.Start, End: seg.End, Node: "s", Port: "p"}

	if !placeAndDrive(pw, 9, bp) {
		t.Fatal("placeAndDrive rejected on fresh wire")
	}

	// Halt, then attempt to advance: a halted FakeClock ignores Advance, so no
	// tick deadline is reached and no position is emitted.
	clk.Halt()
	clk.AdvanceTicks(int64(10 * inFlightMs))
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
	clk.AdvanceTicks(int64(inFlightMs))
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
