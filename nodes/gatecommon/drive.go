package gatecommon

import (
	"context"
	"sync/atomic"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

// DriveHeld runs a continuous-drive goroutine on out, repeatedly emitting
// transform(held.Load()) via out.EmitOneDriven. It is the shared shape behind
// Pulse's and HoldFlip's "hold one atomic value, continuously pulse a
// (possibly transformed) view of it to Out" goroutines: EmitOneDriven is
// synchronous (blocks for the wire traversal), so the goroutine self-paces at
// the wire rate and re-reads held each pulse — when held changes, the next
// pulse carries the new value. Stops when ctx is cancelled or EmitOneDriven
// returns false. transform is applied fresh on every iteration (e.g. identity
// for Pulse, "1-h with NoValue passthrough" for HoldFlip).
func DriveHeld(ctx context.Context, out *Wiring.Out, held *atomic.Int64, transform func(int64) int) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			if !out.EmitOneDriven(ctx, transform(held.Load())) {
				return
			}
		}
	}()
}
