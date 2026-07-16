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
