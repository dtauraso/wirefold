package gatecommon

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

// TestDriveHeldChanModeStillObeysSpeed is the regression proof for the
// gate.go-shaped bug in DriveHeld: before the fix, DriveHeld took its clock
// copy and polled speedCh ONLY when out.Paced() (`paced := out.Paced(); ...
// if paced { c = clk.Copy(); ... }`) — a chan-mode Out (no PacedWire, exactly
// this test's Out) fell back to a raw `time.After` wall sleep that never reads
// speedCh at all, so a speed change sent to it would sit in the channel
// buffer forever, undrained. The fix takes the clock copy and applies speed
// changes UNCONDITIONALLY whenever clk != nil, using `paced` only to choose
// the placement/step strategy. This test proves the clock is live regardless
// of out.Paced(): a real *RealClock + speedCh is handed to a chan-mode
// (unpaced) DriveHeld goroutine, a speed value is sent, and the test asserts
// speedCh gets DRAINED — which can only happen if DriveHeld's own loop is
// calling Wiring.ApplySpeedNonBlocking(c, speedCh) every cycle, i.e. the clock
// path was taken even though out.Paced() == false.
func TestDriveHeldChanModeStillObeysSpeed(t *testing.T) {
	ch := make(chan int, 8)
	out := Wiring.NewOutChanForTest(ch, "n1", "out", nil)

	var held atomic.Int64
	held.Store(7)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clk := Wiring.NewRealClock()
	speedCh := make(chan float64, 1)

	// Chan mode: out.Paced() == false. Before the fix, DriveHeld would never
	// touch clk/speedCh at all in this mode.
	DriveHeld(ctx, out, &held, func(v int64) int { return int(v) }, clk, speedCh)

	// Drain a couple of placements first to confirm the loop is alive and
	// unaffected (chan mode still places every cycle regardless of speed).
	for i := 0; i < 2; i++ {
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
			t.Fatal("DriveHeld did not place any values before the speed send")
		}
	}

	speedCh <- 0

	// Give the goroutine several cycles (MsPerTick == 16ms) to poll and drain
	// speedCh, then check ONCE, non-blockingly, whether it is empty.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(speedCh) == 0 {
			return // drained — the fix is in effect.
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("speedCh was never drained; DriveHeld never applied the speed change " +
		"in chan mode (out.Paced() == false) — the clock-gating bug is back")
}
