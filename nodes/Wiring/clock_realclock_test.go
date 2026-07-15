package Wiring

import (
	"testing"
	"time"
)

// clock_realclock_test.go — behavioral coverage for RealClock (the production Clock).
// Timings sleep a few tick periods (MsPerTick ms each) so the tick actually advances;
// the cancellation path uses an already-cancelled ctx (returns immediately) and the
// reached-target path waits for tick 1, which the wall clock passes within one period.

// TestRealClockTickMonotonic: Tick() never goes backward and advances across a
// sleep of more than one tick period.
func TestRealClockTickMonotonic(t *testing.T) {
	c := NewRealClock()
	a := c.Tick()
	time.Sleep(2 * tickPeriod)
	b := c.Tick()
	if b < a {
		t.Fatalf("Tick() went backward: first=%d second=%d", a, b)
	}
	if b <= a {
		t.Fatalf("Tick() did not advance across a >1-tick sleep: first=%d second=%d", a, b)
	}
}

// TestRealClockHaltFreezesResumeContinues: Halt() freezes the tick at the frozen
// value; Resume() continues from there (no wall-clock catch-up). This is the
// play/pause backing.
func TestRealClockHaltFreezesResumeContinues(t *testing.T) {
	c := NewRealClock()
	time.Sleep(2 * tickPeriod)

	c.Halt()
	frozen := c.Tick()
	// While halted, more wall time passes but the tick must not advance.
	time.Sleep(4 * tickPeriod)
	afterPause := c.Tick()
	if afterPause != frozen {
		t.Fatalf("tick advanced while halted: frozen=%d afterPause=%d", frozen, afterPause)
	}

	c.Resume()
	time.Sleep(2 * tickPeriod)
	afterResume := c.Tick()
	if afterResume <= afterPause {
		t.Fatalf("tick did not continue after Resume: afterPause=%d afterResume=%d", afterPause, afterResume)
	}
	// Continuation is from the frozen point, not wall-clock catch-up: the ~4-tick
	// paused interval must NOT be included, so the tick stays well under the wall
	// time consumed by the test.
	if afterResume > frozen+4 {
		t.Fatalf("Resume appears to have caught up on paused wall time: frozen=%d afterResume=%d", frozen, afterResume)
	}
}

// TestRealClockHaltedHookExactlyOncePerTransition pins the haltedHook contract documented on
// RealClock.haltedHook: Halt() fires the hook with true, Resume() fires with false, and a
// REPEAT call on an already-halted/already-running clock does NOT re-fire — the emit lives
// inside the `if !c.halted` / `if c.halted` guards specifically to get this exactly-once
// property (this is the sole trace-emit point for KindHalted; see Trace.Halted).
func TestRealClockHaltedHookExactlyOncePerTransition(t *testing.T) {
	c := NewRealClock()
	var calls []bool
	c.SetHaltedHook(func(halted bool) {
		calls = append(calls, halted)
	})

	c.Halt()
	c.Halt() // repeat: already halted, must not re-fire
	c.Halt() // repeat again
	if got := calls; len(got) != 1 || got[0] != true {
		t.Fatalf("after 3x Halt(): hook calls = %v, want [true]", got)
	}

	c.Resume()
	c.Resume() // repeat: already running, must not re-fire
	if got := calls; len(got) != 2 || got[0] != true || got[1] != false {
		t.Fatalf("after Resume() x2: hook calls = %v, want [true false]", got)
	}

	c.Halt()
	if got := calls; len(got) != 3 || got[2] != true {
		t.Fatalf("after second real Halt(): hook calls = %v, want [true false true]", got)
	}
}

// TestRealClockNilHookNoPanic verifies that a RealClock with no hook installed (the default —
// headless tests construct clocks without wiring a hook/Trace) does not panic on Halt/Resume.
func TestRealClockNilHookNoPanic(t *testing.T) {
	c := NewRealClock()
	c.Halt()
	c.Halt()
	c.Resume()
	c.Resume()
}
