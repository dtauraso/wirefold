package gatecommon

import (
	"context"
	"sync/atomic"
	"time"

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
// The goroutine paces itself ONE CYCLE AT A TIME via out.StepOnce — it never
// parks inside a full traversal. Each cycle: if no bead is currently in
// flight on out's wire, place the next pulse bead (reading held fresh); then
// sleep one cycle and StepOnce the wire once. Stops when ctx is cancelled or
// a placement fails (wire faded/torn down).
//
// Paced-wire mode (out.Clock() != nil) sleeps on the shared clock's
// SleepCycle so it freezes on pause. Chan mode (out.Clock() == nil, unit
// tests) has no shared clock to sleep on, so it falls back to a wall-clock
// sleep of the same duration.
func DriveHeld(ctx context.Context, out *Wiring.Out, held *atomic.Int64, transform func(int64) int) {
	go func() {
		clk := out.Clock()
		sleep := func(ctx context.Context) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(Wiring.MsPerTick * time.Millisecond):
				return nil
			}
		}
		if clk != nil {
			sleep = clk.SleepCycle
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
			if err := sleep(ctx); err != nil {
				return
			}
			out.StepOnce(ctx)
		}
	}()
}
