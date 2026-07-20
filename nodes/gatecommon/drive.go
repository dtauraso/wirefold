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
// Stops when ctx is cancelled or a placement fails (wire torn down).
//
// Paced-wire mode (out.Paced()) sleeps one WALL cycle per iteration but PACES
// PLACEMENT on the clock's TICK delta: a new bead is placed once the tick has
// advanced by the per-edge K (one edge-crossing latency in ticks) since the last
// placement. Because Tick() is playback-speed-scaled, placement scales WITH speed
// for free — at 2× the tick advances twice as fast so beads are placed twice as
// often, and at 0 the tick holds so no new bead is placed (and the in-flight ones
// don't move either, since StepOnce reads the same frozen tick). Pacing on tick
// delta rather than a wall-cycle COUNT is what makes that true: a wall-cycle
// counter would keep placing at 0 (piling beads at the source) and would not
// speed up at 2×.
//
// Chan mode (!out.Paced(), unit tests) has no shared clock and no wire geometry,
// so it keeps the OLD unconditional per-cycle placement (wall-clock sleep) — that
// mode has no tick to read and no wire to visualize.
// clk is the ORIGIN clock this goroutine Copies from exactly ONCE at its own start
// (docs/planning/visual-editor/per-goroutine-clock.md) — the caller's own Clock field
// (e.g. Pulse/HoldFlip's Node.Clock, injected by reflectBuild), not derived from out
// (port accessors are gone: API demolition item 1). nil in chan mode (unit tests with
// no loader): fine, because clk is never touched unless out.Paced().
func DriveHeld(ctx context.Context, out *Wiring.Out, held *atomic.Int64, transform func(int64) int, clk Wiring.Clock) {
	go func() {
		paced := out.Paced()
		var c Wiring.Clock
		sleep := func(ctx context.Context) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(Wiring.MsPerTick * time.Millisecond):
				return nil
			}
		}
		// tick returns the current tick in paced mode (off this goroutine's own
		// clock copy) or 0 in chan mode, where no caller reads it for anything
		// meaningful (PlaceDrivenAt's chan-mode branch ignores the tick argument).
		tick := func() int64 { return 0 }
		if paced {
			// Copy taken ONCE at this goroutine's start (the go func() literal above
			// IS the goroutine) — docs/planning/visual-editor/per-goroutine-clock.md.
			c = clk.Copy()
			sleep = c.SleepCycle
			tick = c.Tick
		}

		// lastPlaceTick anchors placement pacing in SCALED-tick space (paced mode).
		// Seeded to now so the first bead lands one K after start, as before.
		var lastPlaceTick int64
		if paced {
			lastPlaceTick = tick()
		}
		for {
			if ctx.Err() != nil {
				return
			}

			// Chan mode (!paced): place every cycle exactly as before (immediate
			// send, synchronous chan semantics).
			place := !paced
			if paced {
				if latMs := out.Geom().SimLatencyMs; latMs > 0 {
					k := int64(latMs/Wiring.MsPerTick + 0.999999)
					if k < 1 {
						k = 1
					}
					place = tick()-lastPlaceTick >= k
				}
				// else: geometry not yet known — don't place this cycle.
			}
			if place {
				if out.PlaceDrivenAt(transform(held.Load()), tick()).Failed() {
					return
				}
				if paced {
					lastPlaceTick = tick()
				}
			}

			if err := sleep(ctx); err != nil {
				return
			}
			out.StepOnceAt(ctx, tick())
		}
	}()
}
