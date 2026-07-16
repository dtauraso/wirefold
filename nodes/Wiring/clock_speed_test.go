package Wiring

import (
	"testing"
	"time"
)

// clock_speed_test.go — behavioral coverage for the playback-speed scalar on RealClock.
// These pin the model the slider drives: SetSpeed multiplies tick advance, speed 0 freezes
// the tick, speed 2 doubles it, and Tick() stays continuous (never jumps back) across a
// speed change. Timings sleep several tick periods so the scaled advance is measurable.

// TestClockSpeedFreezeAtZero: at speed 0 the tick does not advance, and it resumes cleanly
// when speed returns to 1 (no wall-clock catch-up for the frozen interval).
func TestClockSpeedFreezeAtZero(t *testing.T) {
	c := NewRealClock()
	time.Sleep(2 * tickPeriod)
	before := c.Tick()

	c.SetSpeed(0)
	frozen := c.Tick()
	time.Sleep(4 * tickPeriod)
	after := c.Tick()
	if after != frozen {
		t.Fatalf("speed 0 did not freeze the tick: frozen=%d after 4 periods=%d", frozen, after)
	}
	if after < before {
		t.Fatalf("tick went backward under freeze: before=%d after=%d", before, after)
	}

	// Resume at 1× and confirm the tick advances again from where it froze.
	c.SetSpeed(1)
	time.Sleep(2 * tickPeriod)
	resumed := c.Tick()
	if resumed <= after {
		t.Fatalf("tick did not resume after speed 1: frozen=%d resumed=%d", after, resumed)
	}
	// The frozen interval must NOT have been credited (no catch-up): resumed should be
	// roughly after + ~2, not after + ~6 (the 4 frozen periods excluded).
	if resumed > after+5 {
		t.Fatalf("speed 0 interval was caught up on resume (should be excluded): after=%d resumed=%d", after, resumed)
	}
}

// TestClockSpeedDoubleAdvancesFaster: over the same wall interval, speed 2 advances the
// tick about twice as far as speed 1.
func TestClockSpeedDoubleAdvancesFaster(t *testing.T) {
	const periods = 8

	c1 := NewRealClock() // speed 1 (default)
	c2 := NewRealClock()
	c2.SetSpeed(2)

	time.Sleep(periods * tickPeriod)

	d1 := c1.Tick()
	d2 := c2.Tick()
	if d2 <= d1 {
		t.Fatalf("speed 2 did not advance faster than speed 1: d1=%d d2=%d", d1, d2)
	}
	// d2 should be ~2× d1. Allow slack for scheduler jitter: require at least 1.5×.
	if float64(d2) < 1.5*float64(d1) {
		t.Fatalf("speed 2 advance not ~2x speed 1: d1=%d d2=%d (want d2 >= 1.5*d1)", d1, d2)
	}
}

// TestClockSpeedContinuousAcrossChange: Tick() is monotonic non-decreasing across a
// mid-run speed change (no backward jump), which is what keeps a bead's fractional
// progress continuous when the slider moves.
func TestClockSpeedContinuousAcrossChange(t *testing.T) {
	c := NewRealClock()
	time.Sleep(3 * tickPeriod)
	a := c.Tick()
	c.SetSpeed(2)
	b := c.Tick()
	if b < a {
		t.Fatalf("tick jumped backward across a speed change: before=%d after=%d", a, b)
	}
	c.SetSpeed(0)
	time.Sleep(2 * tickPeriod)
	c.SetSpeed(1)
	d := c.Tick()
	if d < b {
		t.Fatalf("tick jumped backward across freeze+resume: %d then %d", b, d)
	}
}
