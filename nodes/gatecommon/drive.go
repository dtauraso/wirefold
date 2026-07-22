package gatecommon

import (
	"context"
	"time"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

// DriveHeld runs a continuous-drive goroutine on out, repeatedly emitting
// transform(cur), where cur is this goroutine's OWN local copy of the held
// value, kept current by draining heldCh non-blocking once per cycle. It is
// the shared shape behind Pulse's and HoldFlip's "hold one value (owned by
// the caller's main loop), continuously pulse a (possibly transformed) view
// of it to Out" goroutines. transform is applied fresh on every iteration
// (e.g. identity for Pulse, "1-h with NoValue passthrough" for HoldFlip);
// draining heldCh each cycle means when the main loop sends a new held value,
// the next pulse carries it.
//
// heldCh is a buffered-1, latest-wins channel (same shape as speedCh below):
// the caller's main loop OWNS held and sends it non-blocking via
// Wiring.SendLatestNonBlocking whenever it changes; this goroutine is the
// sole reader. cur is seeded to gatecommon.NoValue (the same seed the caller's
// main loop stores into held before spawning this goroutine) and only
// changes when heldCh actually delivers a value — this preserves the old
// atomic.Load() behavior exactly, since the main loop's pre-spawn seed and
// this goroutine's local seed start equal.
//
// The wire's own goroutine advances in-flight beads every cycle (one
// position-step per tick, never jumping); this goroutine only PLACES a new
// bead once per this edge's OWN tick-count period, `K = ticksToCross =
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
// Chan mode (!out.Paced(), unit tests) has no wire to advance, so it keeps the
// OLD unconditional per-cycle placement (synchronous chan send) — there is no
// wire geometry (K) to pace placement against. It may still have a REAL clock
// copy (clk != nil, e.g. a test that constructs one directly, or a production
// Out that simply has no wire attached in this topology) — see the clk param
// doc below: the clock is taken and kept speed-aware regardless of out.Paced(),
// exactly like RunGate (nodes/gatecommon/gate.go) — only the placement/step
// STRATEGY (wire-tick-paced vs. per-cycle chan) depends on out.Paced().
//
// clk is the ORIGIN clock this goroutine Copies from exactly ONCE at its own start
// — the caller's own Clock field
// (e.g. Pulse/HoldFlip's Node.Clock, injected by reflectBuild), not derived from out
// (port accessors are gone: API demolition item 1). nil only on a genuinely
// clock-less build (unit tests with no loader): DriveHeld then falls back to a
// raw wall-clock sleep and never applies a speed change, because there is no
// clock to apply one to. Whenever clk is non-nil it is Copied and kept
// speed-aware UNCONDITIONALLY — out.Paced() must NOT gate this (that was the
// bug: an Out with no wire fell back to a wall-clock sleep deaf to the
// playback-speed slider, the same shape RunGate was fixed for in gate.go).
//
// speedCh delivers a speed change to THIS goroutine's own clock copy
// (per-goroutine-clock.md "Delivery"). Each DriveHeld call spawns an
// INDEPENDENT goroutine with its own clock copy, so a node driving two Outs
// (Pulse's Out/Out2, or any future fan-out) must pass a DIFFERENT channel per
// call — passing the same channel to two DriveHeld goroutines would starve
// whichever one loses a given receive. nil is fine (chan mode, or a caller
// with no speed channel to give): ApplySpeedNonBlocking is then a no-op.
func DriveHeld(ctx context.Context, out *Wiring.Out, heldCh <-chan int64, transform func(int64) int, clk Wiring.Clock, speedCh <-chan float64) {
	go func() {
		paced := out.Paced()
		cur := int64(NoValue)
		var c Wiring.Clock
		sleep := func(ctx context.Context) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(Wiring.MsPerTick * time.Millisecond):
				return nil
			}
		}
		// tick returns the current tick off this goroutine's own clock copy
		// whenever one exists (clk != nil), or 0 on a genuinely clock-less build
		// (unit tests with no loader). Used only to pace placement against K
		// below; the wire itself now stamps its own placementTick when it drains
		// the send, independent of this reading. This must NOT be gated on
		// `paced` — an Out with no wire but a real clock copy still has to stay
		// speed-aware (see the doc comment above).
		tick := func() int64 { return 0 }
		if clk != nil {
			// Copy taken ONCE at this goroutine's start (the go func() literal above
			// IS the goroutine).
			c = clk.Copy()
			// Fold the speed-delivery poll into the one blocking point this loop has
			// (this comment block's own note above it): DriveHeld's only blocking
			// point is sleep, so that is where the check goes.
			sleep = func(ctx context.Context) error {
				Wiring.ApplySpeedNonBlocking(c, speedCh)
				return c.SleepCycle(ctx)
			}
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

			// Drain heldCh to latest, non-blocking, once per cycle — the
			// caller's main loop is the sole sender (buffered-1, latest-wins),
			// so a single non-blocking receive is always enough to catch up.
			select {
			case v := <-heldCh:
				cur = v
			default:
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
				di := out.PlaceDrivenAt(transform(cur))
				if di.Failed() {
					return
				}
				// DriveBufferFull is TRANSIENT (the paced wire's inCh was
				// momentarily full) — do not stop the loop or advance
				// lastPlaceTick; retry the placement next cycle instead of
				// silently losing this drive goroutine forever.
				if !di.BufferFull() && paced {
					lastPlaceTick = tick()
				}
			}

			if err := sleep(ctx); err != nil {
				return
			}
		}
	}()
}
