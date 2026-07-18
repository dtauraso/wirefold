package Wiring

import (
	"context"
	"math"
	"time"
)

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

// approxEq is the float tolerance used by geometry/position wire tests.
func approxEq(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
