package gatecommon

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

// TestDriveHeldChanModePlacesMultipleValues is the red-proof for the
// DriveItem outcome fix: before the fix, PlaceDrivenAt's chan-mode successful
// send returned an inert DriveItem{} indistinguishable from a failed/torn-down
// placement, so DriveHeld's `if !out.PlaceDrivenAt(...).Live() { return }`
// treated every chan-mode send as a failure and stopped after exactly one
// placement. A chan-mode Out driven by DriveHeld must keep placing values
// across multiple cycles.
func TestDriveHeldChanModePlacesMultipleValues(t *testing.T) {
	ch := make(chan int, 8)
	out := Wiring.NewOutChanForTest(ch, "n1", "out", nil)

	var held atomic.Int64
	held.Store(7)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Chan mode (out.Paced() == false): DriveHeld never touches clk/speedCh, so nil is safe.
	DriveHeld(ctx, out, &held, func(v int64) int { return int(v) }, nil, nil)

	received := 0
	deadline := time.After(2 * time.Second)
	for received < 3 {
		select {
		case v := <-ch:
			if v != 7 {
				t.Fatalf("got value %d, want 7", v)
			}
			received++
		case <-deadline:
			t.Fatalf("timed out after receiving only %d value(s); DriveHeld stopped placing (chan-mode send misread as failure)", received)
		}
	}
}
