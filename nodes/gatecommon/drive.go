package gatecommon

import (
	"context"
	"sync/atomic"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

// DriveHeld runs a continuous-drive goroutine on out, repeatedly emitting
// transform(held.Load()). It is the shared shape behind Pulse's and HoldFlip's
// "hold one atomic value, continuously pulse a (possibly transformed) view of
// it to Out" goroutines. transform is applied fresh on every iteration (e.g.
// identity for Pulse, "1-h with NoValue passthrough" for HoldFlip); re-reading
// held each pulse means when held changes, the next pulse carries the new
// value.
//
// Paced-wire mode (out.Clock() != nil): the goroutine paces itself ONE TICK
// AT A TIME via out.StepOnce — it never parks inside a full traversal. Each
// cycle: if no bead is currently in flight on out's wire, place the next
// pulse bead (reading held fresh) at the CURRENT tick — this is the same
// instant the previous pulse's blocking drive used to return and place the
// next one, so pulse cadence/spacing is unchanged; then wait for the next
// tick and StepOnce the wire once. Repeating StepOnce once per tick
// reproduces the same per-tick trajectory (and therefore the same delivery
// tick) as the old blocking DriveBeadToDelivery path — see PacedWire.StepOnce.
// Stops when ctx is cancelled or a placement fails (wire faded/torn down).
//
// Chan mode (out.Clock() == nil, unit tests): there is no wire clock to pace
// against, so this falls back to the previous synchronous EmitOneDriven loop
// (EmitOneDriven's chan-mode branch is already a non-blocking select).
func DriveHeld(ctx context.Context, out *Wiring.Out, held *atomic.Int64, transform func(int64) int) {
	go func() {
		clk := out.Clock()
		if clk == nil {
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
		}

		for {
			if ctx.Err() != nil {
				return
			}
			if !out.InFlight() {
				if !out.PlaceDriven(transform(held.Load())).Live() {
					return
				}
			}
			if err := clk.WaitTick(ctx, clk.Tick()+1); err != nil {
				return
			}
			out.StepOnce(ctx)
		}
	}()
}
