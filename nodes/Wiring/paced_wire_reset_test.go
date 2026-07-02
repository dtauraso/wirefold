package Wiring

import (
	"testing"
)

// paced_wire_reset_test.go — Reset() drops all in-flight and delivered beads (via
// teardownLocked) and invalidates outstanding driven loops. Contract: Reset itself emits
// NOTHING (teardownLocked RETURNS the cancelled arriveInfos for the CALLER to emit; Reset
// is the edge-delete internal that only clears state). We assert both queues empty after.

func TestResetClearsInFlightAndDelivered(t *testing.T) {
	pw, clk := newFakeWire()

	// Place several beads, then deliver one so both queues are populated.
	for _, v := range []int{1, 2, 3} {
		if !placeAndDrive(pw, v, beadPlacement{InFlightMs: testInFlightMs}) {
			t.Fatalf("placeAndDrive %d returned false", v)
		}
	}
	waitDelivered(t, pw, clk, 1) // advance so >=1 bead lands in delivered

	pw.mu.Lock()
	preIn, preDel := len(pw.inflight), len(pw.delivered)
	pw.mu.Unlock()
	if preIn == 0 && preDel == 0 {
		t.Fatal("precondition: expected some beads before Reset")
	}

	pw.Reset()

	pw.mu.Lock()
	in, del := len(pw.inflight), len(pw.delivered)
	pw.mu.Unlock()
	if in != 0 || del != 0 {
		t.Fatalf("Reset left beads: inflight=%d delivered=%d", in, del)
	}
}

// TestResetOnEmptyWireNoOp: Reset on a fresh wire is a safe no-op (queues stay empty).
func TestResetOnEmptyWireNoOp(t *testing.T) {
	pw, _ := newFakeWire()
	pw.Reset()
	pw.mu.Lock()
	in, del := len(pw.inflight), len(pw.delivered)
	pw.mu.Unlock()
	if in != 0 || del != 0 {
		t.Fatalf("Reset on empty wire produced beads: inflight=%d delivered=%d", in, del)
	}
}
