package Wiring

import (
	"context"
	"testing"
	"time"
)

// testInFlightMs is the per-bead in-flight time used across these wire tests.
// Delivery is timed on the (fake) clock: place a bead, Advance the clock by this
// amount, and the wire delivers it into the delivered FIFO.
const testInFlightMs = 50

// newFakeWire builds a PacedWire backed by a FakeClock the test advances.
func newFakeWire() (*PacedWire, *FakeClock) {
	pw := NewPacedWire(100, PulseSpeedWuPerMs)
	clk := NewFakeClock()
	pw.SetClock(clk)
	return pw, clk
}

// placeAndDrive places a bead WITHOUT a walker and drives it to delivery on a
// background goroutine that sleeps one cycle then StepOnces, matching the
// production per-cycle StepOnce delivery path (no blocking delivery loop).
// clk.AdvanceTicks then moves the bead into the delivered FIFO exactly as
// before.
func placeAndDrive(pw *PacedWire, val int, bp beadPlacement) bool {
	gen, ok := pw.placeBeadNoWalker(val, bp)
	if !ok {
		return false
	}
	go driveGenToDelivery(pw, gen)
	return true
}

// driveGenToDelivery repeatedly StepOnces pw until the bead identified by gen
// is no longer in flight (delivered or torn down). It polls on a short
// wall-clock sleep rather than blocking on the fake clock's SleepCycle/WaitTick:
// a FakeClock-driven test typically issues ONE AdvanceTicks jump that can land
// before this goroutine is even scheduled, and a target computed relative to
// "now" at that point would then wait for a tick that never comes. Polling
// means every AdvanceTicks jump — whenever it happens relative to this
// goroutine's scheduling — is picked up by the next StepOnce, which always
// reads the clock live.
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

// waitDelivered advances clk past one bead's in-flight time and waits until at
// least `want` values sit in the delivered FIFO (a bead landed). timeout guards
// against a missed wake.
func waitDelivered(t *testing.T, pw *PacedWire, clk *FakeClock, want int) {
	t.Helper()
	clk.AdvanceTicks(testInFlightMs)
	deadline := time.Now().Add(time.Second)
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
