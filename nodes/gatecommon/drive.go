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
// The goroutine steps the wire EVERY cycle (so in-flight beads keep gliding
// one position-step per tick, never jumping) but only PLACES a new bead once
// per this edge's OWN tick-count period, `K = ticksToCross =
// SimLatencyMs/MsPerTick` (same formula and ceil-rounding convention as
// holdnewsendold/node.go's ToNext processing window) — one placement per
// full crossing, so a wire carries roughly one resident bead rather than one
// per tick. Per MODEL.md §Sending this is still legal: K is read from the
// edge's own GEOMETRY (a static formula over arc length/pulse speed), never
// from the wire's occupancy — DriveHeld never asks the wire "are you busy?".
// Geom() is re-read fresh every iteration (not cached at startup) because a
// drag can change the edge's length, and K must track it.
//
// K is clamped to at least 1 tick. If SimLatencyMs is not yet known (zero —
// a real paced Out's geometry is seeded at construction from the loader's
// arc/latency, so this only happens transiently before that seed) DriveHeld
// does not place until it is; it still steps the wire every cycle, so
// nothing else stalls waiting on it.
//
// Stops when ctx is cancelled or a placement fails (wire faded/torn down).
//
// Paced-wire mode (out.Clock() != nil) sleeps on the shared clock's
// SleepCycle so it freezes on pause, and paces placement on the per-edge K
// above. Chan mode (out.Clock() == nil, unit tests) has no shared clock and
// no wire geometry, so it keeps the OLD unconditional per-cycle placement
// (falls back to a wall-clock sleep of the same duration) — that mode never
// exhibited the density bug since it has no wire to visualize.
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

		var cyclesSincePlace int64 = 0
		for {
			if ctx.Err() != nil {
				return
			}

			// Chan mode (clk == nil, unit tests): no wire geometry, no shared
			// clock — place every cycle exactly as before (immediate send,
			// synchronous chan semantics).
			place := clk == nil
			if clk != nil {
				if latMs := out.Geom().SimLatencyMs; latMs > 0 {
					k := int64(latMs/Wiring.MsPerTick + 0.999999)
					if k < 1 {
						k = 1
					}
					place = cyclesSincePlace >= k
				}
				// else: geometry not yet known — don't place this cycle.
			}
			if place {
				if !out.PlaceDriven(transform(held.Load())).Live() {
					return
				}
				cyclesSincePlace = 0
			}

			if err := sleep(ctx); err != nil {
				return
			}
			out.StepOnce(ctx)
			cyclesSincePlace++
		}
	}()
}
