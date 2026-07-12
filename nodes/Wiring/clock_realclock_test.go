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
