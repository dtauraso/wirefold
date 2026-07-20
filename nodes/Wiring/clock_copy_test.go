package Wiring

import (
	"testing"
	"time"
)

// clock_copy_test.go — pins the property the whole per-goroutine-clock change rests
// on: with RealClock.mu deleted,
// RealClock is a plain value type, and a dereference-copy (`c2 := *c1`) is how a
// goroutine gets its OWN clock rather than reaching a shared one through a pointer.
// Two properties fall out of "copy, don't share":
//
//  1. Independence — SetSpeed on one copy must not move another copy's rate. A
//     shared *RealClock fails this immediately (both "copies" are the same object).
//  2. Agreement — two copies taken from the same origin must report the same Tick()
//     for the same instant, because Tick() is a pure function of origin + speed
//     history + now (see per-goroutine-clock.md "Why copies agree"). This is what
//     lets N independent copies act like one clock without sharing memory.

// TestRealClockCopyIndependent: SetSpeed on a copy must not affect the original's
// rate, and vice versa. Deliberately made RED once (see comment below) by making
// "the copy" a shared *RealClock instead of a value copy, confirming the assertion
// actually detects sharing rather than passing regardless.
func TestRealClockCopyIndependent(t *testing.T) {
	orig := NewRealClock()
	cp := *orig // value copy: cp shares no memory with orig from this point on.

	// Freeze the copy; the original must keep advancing.
	cp.SetSpeed(0)
	time.Sleep(3 * tickPeriod)

	if got := cp.Tick(); got != 0 {
		t.Fatalf("frozen copy advanced: Tick()=%d, want 0", got)
	}
	if got := orig.Tick(); got < 2 {
		t.Fatalf("original did not keep advancing while copy was frozen: Tick()=%d, want >=2", got)
	}

	// And the reverse: freeze the original, the copy (already frozen above) stays
	// independent — bump the copy's speed up and confirm the original does NOT
	// follow it.
	orig.SetSpeed(0)
	cp.SetSpeed(4)
	origBefore := orig.Tick()
	time.Sleep(3 * tickPeriod)
	if got := orig.Tick(); got != origBefore {
		t.Fatalf("frozen original advanced after copy's SetSpeed: before=%d after=%d", origBefore, got)
	}
}

// TestRealClockCopiesAgree: two copies made from the same clock, given the same
// speed history, must report the same Tick() for the same instant — pinning that
// copying carries the origin (accScaled/lastChange/speed), not just the type.
func TestRealClockCopiesAgree(t *testing.T) {
	base := NewRealClock()
	time.Sleep(2 * tickPeriod) // let some real elapsed accumulate before copying.
	a := *base
	b := *base

	time.Sleep(2 * tickPeriod)
	ta, tb := a.Tick(), b.Tick()
	if ta != tb {
		t.Fatalf("copies from the same origin disagree: a=%d b=%d", ta, tb)
	}
}
