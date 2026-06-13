package Wiring

import (
	"testing"
	"time"
)

// trainBeadInFlightMs keeps each placed bead in flight long past the whole train
// window (2000 ms), so a count of inflight+delivered equals total beads PLACED —
// the property these tests assert. (Delivery timing is covered elsewhere.)
const trainBeadInFlightMs = 100000

// placedCount returns how many beads the train has placed so far (in flight plus
// already delivered). Used instead of exact-ms timing so the assertions are on
// counts/values, not flaky wall-clock.
func placedCount(pw *PacedWire) int {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	return len(pw.inflight) + len(pw.delivered)
}

// waitPlaced spins until the pacer goroutine has placed `want` beads (or fails).
// The pacer runs concurrently with the test; after an Advance we must let it wake.
func waitPlaced(t *testing.T, pw *PacedWire, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		if placedCount(pw) >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("train placed %d beads, expected >= %d", placedCount(pw), want)
		}
		time.Sleep(time.Millisecond)
	}
}

func trainPlacement() beadPlacement {
	return beadPlacement{InFlightMs: trainBeadInFlightMs}
}

// TestTrainPacedEmission: one fire (StartTrain) places the first bead at once,
// then one bead every beadSpacingMs for trainDurationMs — ~5 beads total
// (at 0, 400, 800, 1200, 1600, 2000 ms). After the window closes no more beads
// are placed even as the clock keeps advancing. All beads carry the SAME value.
func TestTrainPacedEmission(t *testing.T) {
	pw, clk := newFakeWire()

	if !pw.StartTrain(7, trainPlacement()) {
		t.Fatal("StartTrain returned false on a live wire")
	}
	// First bead places immediately (no initial delay).
	waitPlaced(t, pw, 1)
	if got := placedCount(pw); got != 1 {
		t.Fatalf("immediately after fire: expected 1 bead, got %d", got)
	}

	// One bead per beadSpacingMs across the trainDurationMs window. Beads land at
	// 0,400,...,2000 ms ⇒ trainDurationMs/beadSpacingMs + 1 = 6 placements.
	steps := trainDurationMs / beadSpacingMs // 5
	for i := 1; i <= steps; i++ {
		clk.Advance(beadSpacingMs * time.Millisecond)
		waitPlaced(t, pw, i+1)
	}
	want := steps + 1 // first bead + one per step
	if got := placedCount(pw); got != want {
		t.Fatalf("after full window: expected %d beads, got %d", want, got)
	}

	// Window closed: further clock advances place NO additional beads.
	clk.Advance(5 * beadSpacingMs * time.Millisecond)
	time.Sleep(20 * time.Millisecond) // give the pacer a chance to (not) place
	if got := placedCount(pw); got != want {
		t.Fatalf("after window closed: expected %d beads (no new), got %d", want, got)
	}

	// Every placed bead carried the same value.
	pw.mu.Lock()
	for i, b := range pw.inflight {
		if b.val != 7 {
			t.Fatalf("bead %d: expected value 7, got %d", i, b.val)
		}
	}
	pw.mu.Unlock()
}

// TestTrainRefreshSwitchesValueAndResetsWindow: a re-fire mid-train with a new
// value switches the placed value AND resets the trainDurationMs window (it does
// not stack a second overlapping pacer). The new value's beads keep coming for a
// fresh trainDurationMs from the refresh instant.
func TestTrainRefreshSwitchesValueAndResetsWindow(t *testing.T) {
	pw, clk := newFakeWire()

	pw.StartTrain(0, trainPlacement())
	waitPlaced(t, pw, 1)

	// Advance two spacings into the train (value 0 beads placed at 0,400,800).
	clk.Advance(beadSpacingMs * time.Millisecond)
	waitPlaced(t, pw, 2)
	clk.Advance(beadSpacingMs * time.Millisecond)
	waitPlaced(t, pw, 3)

	// Re-fire mid-train with a new value: refresh, not a second pacer.
	pw.StartTrain(1, trainPlacement())
	waitPlaced(t, pw, 4) // refresh places its first bead immediately

	// The refresh placed value 1 immediately; the just-placed bead is value 1.
	pw.mu.Lock()
	last := pw.inflight[len(pw.inflight)-1].val
	pw.mu.Unlock()
	if last != 1 {
		t.Fatalf("refresh: expected newest bead value 1, got %d", last)
	}

	// Window was reset: value-1 beads keep coming for a fresh trainDurationMs.
	// Advance a full window worth from the refresh; expect one bead per spacing.
	before := placedCount(pw)
	steps := trainDurationMs / beadSpacingMs
	for i := 0; i < steps; i++ {
		clk.Advance(beadSpacingMs * time.Millisecond)
		waitPlaced(t, pw, before+i+1)
	}

	// All beads placed AFTER the refresh carry value 1 (only one pacer running).
	pw.mu.Lock()
	defer pw.mu.Unlock()
	v1 := 0
	for _, b := range pw.inflight {
		if b.val == 1 {
			v1++
		}
	}
	if v1 < steps+1 {
		t.Fatalf("expected >= %d value-1 beads after refresh, got %d", steps+1, v1)
	}
}

// TestTrainPauseFreezesPacer: while the global gate is halted, the pacer does not
// advance/place; resuming continues the train. Mirrors the wire's pause gate.
func TestTrainPauseFreezesPacer(t *testing.T) {
	pw, clk := newFakeWire()

	pw.StartTrain(5, trainPlacement())
	waitPlaced(t, pw, 1)

	clk.Halt()
	// While halted, Advance is a no-op (pause stops the arithmetic): no new beads.
	clk.Advance(3 * beadSpacingMs * time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	if got := placedCount(pw); got != 1 {
		t.Fatalf("while halted: expected 1 bead (frozen), got %d", got)
	}

	// Resume and advance one spacing: the pacer continues placing.
	clk.Resume()
	clk.Advance(beadSpacingMs * time.Millisecond)
	waitPlaced(t, pw, 2)
}
