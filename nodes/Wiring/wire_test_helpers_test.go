package Wiring

import (
	"context"
	"math"
	"testing"
	"time"
)

// testInFlightMs is the per-bead in-flight time used across these wire tests.
// Delivery is timed on the one real clock: place a bead and the wire delivers it
// into the delivered FIFO once real time carries the tick past the bead's
// deadline (driveGenToDelivery StepOnces it there).
const testInFlightMs = 50

// newTestWire builds a PacedWire backed by the single real clock (the model is
// sleep-only; there is no FakeClock). Delivery is driven by real-time StepOnce
// polling, not by manually advancing a clock.
func newTestWire() (*PacedWire, *RealClock) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	clk := NewRealClock()
	pw.SetClock(clk)
	return pw, clk
}

// newFakeWire is retained as an alias for the historic helper name used by wire
// tests; it now returns the real clock.
func newFakeWire() (*PacedWire, *RealClock) { return newTestWire() }

// placeAndDrive places a bead WITHOUT a walker and drives it to delivery on a
// background goroutine that StepOnces on a short wall-clock poll, matching the
// production per-cycle StepOnce delivery path (no blocking delivery loop). The
// real clock advances on its own, so each StepOnce reads a later tick until the
// bead's deadline is crossed and it lands in the delivered FIFO.
func placeAndDrive(pw *PacedWire, val int, bp beadPlacement) bool {
	gen, ok := pw.placeBeadNoWalker(val, bp)
	if !ok {
		return false
	}
	go driveGenToDelivery(pw, gen)
	return true
}

// driveGenToDelivery repeatedly StepOnces pw until the bead identified by gen is
// no longer in flight (delivered or torn down). It polls on a short wall-clock
// sleep; each StepOnce reads the live real clock, so the bead advances as real
// time carries the tick forward.
func driveGenToDelivery(pw *PacedWire, gen uint64) {
	ctx := context.Background()
	for {
		pw.mu.Lock()
		idx := pw.findInflightLocked(gen)
		pw.mu.Unlock()
		if idx < 0 {
			return
		}
		pw.StepOnce(ctx)
		time.Sleep(time.Millisecond)
	}
}

// waitDelivered waits until at least `want` values sit in the delivered FIFO (a
// bead landed). The real clock advances on its own while driveGenToDelivery
// steps the bead to its deadline; timeout guards against a missed delivery.
func waitDelivered(t *testing.T, pw *PacedWire, clk *RealClock, want int) {
	t.Helper()
	_ = clk // real clock advances on its own; no manual stepping needed
	deadline := time.Now().Add(2 * time.Second)
	for {
		pw.mu.Lock()
		n := len(pw.delivered)
		pw.mu.Unlock()
		if n >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("waitDelivered: timed out waiting for %d delivered (have %d)", want, n)
		}
		time.Sleep(time.Millisecond)
	}
}

// approxEq is the float tolerance used by geometry/position wire tests.
func approxEq(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
