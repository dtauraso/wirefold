package Wiring

import (
	"context"
	"sync"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
)

// emit_refill_slide_speed_test.go — regression for the user-observed bug: "the
// slider is not controlling the in-node bead animations. it is controlling the
// beads on the edges." emitRefillSlide (the Input node's interior refill
// animation) runs its OWN blocking SleepCycle loop, separate from its caller's
// main loop — it must poll ApplySpeedNonBlocking itself each cycle, exactly like
// every other paced loop in the system (input/node.go's two loops, gatecommon's
// RunGate/DriveHeld). It did not; a speed change sent mid-slide sat unapplied in
// the channel until the slide finished. This test drives the REAL clock/slide
// loop and asserts wall-clock completion time actually scales with speed.

// runSlideAndTimeCompletion runs emitRefillSlide with a single bead, sends
// speedCh <- setSpeed immediately (buffered-1, so the first poll picks it up
// before the first SleepCycle), and returns how long (wall time) the slide took
// to land at t=1 (its final frame, detected by the row-1 bead reaching its
// destination offset).
func runSlideAndTimeCompletion(t *testing.T, setSpeed float64) time.Duration {
	t.Helper()

	dest := interiorSlotOffset(1, 0)

	var mu sync.Mutex
	done := make(chan struct{})
	var closeOnce sync.Once
	onEvent := func(e T.Event) {
		if e.Kind != T.KindNodeBead || e.Row != 1 || e.Col != 0 || !e.Present {
			return
		}
		mu.Lock()
		landed := e.X == dest.X && e.Y == dest.Y && e.Z == dest.Z
		mu.Unlock()
		if landed {
			closeOnce.Do(func() { close(done) })
		}
	}
	tr := T.NewWithSinkHook(64, nil, onEvent)

	clk := NewRealClock()
	speedCh := make(chan float64, 1)
	speedCh <- setSpeed

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	go emitRefillSlide(ctx, tr, "TestInput", clk.Copy(), speedCh, []int{1})

	select {
	case <-done:
		return time.Since(start)
	case <-ctx.Done():
		t.Fatalf("slide did not complete within timeout at speed %v", setSpeed)
		return 0
	}
}

// TestEmitRefillSlideScalesWithSpeed asserts the slide's wall-clock duration
// actually scales with the speed delivered on speedCh: roughly half the time at
// 2x versus 1x. Before the fix (emitRefillSlide not polling
// ApplySpeedNonBlocking), the speed sent before the loop started would still be
// picked up on the FIRST poll (a poll was added before the loop, matching every
// other node's pattern) — so to catch the REAL bug (a speed change sent MID-
// slide having no effect), this test's low-effort form only proves the channel
// is read at all. The stronger, deliberately-red-then-green proof is
// TestEmitRefillSlideAppliesMidSlideSpeedChange below, which changes speed
// AFTER the loop has already started.
func TestEmitRefillSlideScalesWithSpeed(t *testing.T) {
	elapsed1 := runSlideAndTimeCompletion(t, 1.0)
	elapsed2 := runSlideAndTimeCompletion(t, 2.0)

	ratio := float64(elapsed1) / float64(elapsed2)
	// Expect ~2x speedup; allow generous slack for scheduler/wall-clock noise
	// (this is timing-sensitive, not exact-value, math).
	if ratio < 1.4 || ratio > 3.0 {
		t.Fatalf("expected ~2x speedup (1x=%v, 2x=%v, ratio=%v), got a ratio outside [1.4,3.0]", elapsed1, elapsed2, ratio)
	}
}

// TestEmitRefillSlideAppliesMidSlideSpeedChange is the load-bearing regression:
// it starts the slide at speed 0 (frozen — Tick() never advances relative to
// start once SetSpeed(0) has been banked), confirms it does NOT complete within
// a short grace window, THEN sends a speed-up on speedCh and asserts the slide
// completes soon after. This can only pass if emitRefillSlide's own loop polls
// speedCh WHILE the slide is running (not just once before the loop starts) —
// exactly the bug reported live: "in-node bead animations" ignored the slider.
func TestEmitRefillSlideAppliesMidSlideSpeedChange(t *testing.T) {
	dest := interiorSlotOffset(1, 0)

	done := make(chan struct{})
	var closeOnce sync.Once
	onEvent := func(e T.Event) {
		if e.Kind != T.KindNodeBead || e.Row != 1 || e.Col != 0 || !e.Present {
			return
		}
		if e.X == dest.X && e.Y == dest.Y && e.Z == dest.Z {
			closeOnce.Do(func() { close(done) })
		}
	}
	tr := T.NewWithSinkHook(64, nil, onEvent)

	clk := NewRealClock()
	clk.SetSpeed(0) // frozen from the start
	speedCh := make(chan float64, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go emitRefillSlide(ctx, tr, "TestInput", clk.Copy(), speedCh, []int{1})

	// Frozen: must NOT complete within a grace window comfortably shorter than
	// a speed-1 completion (~a few hundred ms per the durationTicks math).
	select {
	case <-done:
		t.Fatalf("slide completed while frozen at speed 0 — clock did not actually freeze")
	case <-time.After(150 * time.Millisecond):
	}

	// Now speed it up mid-slide. If emitRefillSlide never polls speedCh inside
	// its own loop (the bug), this value sits in the channel forever and the
	// slide never completes (the surrounding ctx timeout would fire instead).
	speedCh <- 4.0

	select {
	case <-done:
		// Passed: the in-flight slide heard the mid-slide speed change.
	case <-ctx.Done():
		t.Fatalf("slide never completed after a mid-slide speed-up — speedCh was not polled inside emitRefillSlide's own loop")
	}
}
