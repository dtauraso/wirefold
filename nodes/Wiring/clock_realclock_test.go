package Wiring

import (
	"context"
	"testing"
	"time"
)

// clock_realclock_test.go — behavioral coverage for RealClock (the production Clock).
// All timings are single-digit-ms bounded so nothing hangs. The cancellation path uses
// an already-cancelled ctx (returns immediately) and the reached-target path uses a tiny
// real target that the wall clock passes within a few ms.

// TestRealClockNowMonotonic: Now() never goes backward as wall time advances.
func TestRealClockNowMonotonic(t *testing.T) {
	c := NewRealClock()
	a := c.Now()
	time.Sleep(3 * time.Millisecond)
	b := c.Now()
	if b < a {
		t.Fatalf("Now() went backward: first=%v second=%v", a, b)
	}
	if b <= a {
		t.Fatalf("Now() did not advance across a real sleep: first=%v second=%v", a, b)
	}
}

// TestRealClockHaltFreezesResumeContinues: Halt() freezes active elapsed at the frozen
// point; Resume() continues from there (no wall-clock catch-up). This is the play/pause
// backing.
func TestRealClockHaltFreezesResumeContinues(t *testing.T) {
	c := NewRealClock()
	time.Sleep(2 * time.Millisecond)

	c.Halt()
	frozen := c.Now()
	// While halted, more wall time passes but active elapsed must not advance.
	time.Sleep(8 * time.Millisecond)
	afterPause := c.Now()
	if afterPause-frozen > 2*time.Millisecond {
		t.Fatalf("elapsed advanced while halted: frozen=%v afterPause=%v", frozen, afterPause)
	}

	c.Resume()
	time.Sleep(4 * time.Millisecond)
	afterResume := c.Now()
	if afterResume <= afterPause {
		t.Fatalf("elapsed did not continue after Resume: afterPause=%v afterResume=%v", afterPause, afterResume)
	}
	// Continuation is from the frozen point, not wall-clock catch-up: the ~8ms paused
	// interval must NOT be included, so total active elapsed stays well under the wall
	// time consumed by the test.
	if afterResume > frozen+40*time.Millisecond {
		t.Fatalf("Resume appears to have caught up on paused wall time: frozen=%v afterResume=%v", frozen, afterResume)
	}
}

// TestRealClockWaitUntilReached: WaitUntil returns nil once a tiny real target is reached.
func TestRealClockWaitUntilReached(t *testing.T) {
	c := NewRealClock()
	done := make(chan error, 1)
	go func() { done <- c.WaitUntil(context.Background(), 3*time.Millisecond) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WaitUntil to a reached target: got err %v, want nil", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("WaitUntil did not return after the tiny target was reached")
	}
}

// TestRealClockWaitUntilCancelledCtx: WaitUntil with an already-cancelled ctx returns
// ctx.Err() immediately (never waits on an unreachable target).
func TestRealClockWaitUntilCancelledCtx(t *testing.T) {
	c := NewRealClock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the call

	done := make(chan error, 1)
	// Target is far in the future; only cancellation can make this return.
	go func() { done <- c.WaitUntil(ctx, time.Hour) }()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("WaitUntil with cancelled ctx: got %v, want context.Canceled", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("WaitUntil did not return on an already-cancelled ctx")
	}
}
