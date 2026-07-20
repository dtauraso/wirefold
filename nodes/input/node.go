package input

import (
	"context"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

type Node struct {
	Wiring.LayoutHolder
	Fire         func()
	EmitGeometry func()
	// EmitNodeBeads streams the live interior buffer (2x2 grid) as node-bead
	// events — one per present bead. Injected by Wiring.reflectBuild (captures this
	// node's geometry). Called whenever working/backup change so the emitted set
	// always reflects the live arrays. Discrete positions only this phase.
	EmitNodeBeads func(working, backup []int)
	// EmitRefillSlide runs the clock-paced animated refill: the OLD backup (top
	// row) slides DOWN into the working (bottom) row at human speed. Injected by
	// Wiring.reflectBuild; the caller supplies the CLOCK and SPEED CHANNEL at call
	// time (its own already-Copy()'d clock and its own n.SpeedCh — see
	// updateFeedbackRing's n.EmitRefillSlide(clk, n.SpeedCh, *backup) call), so
	// this closure captures only this node's id + geometry, never a clock — see
	// per-goroutine-clock.md's note on the old shape (a captured shared clock read
	// on every call) being a residual to close, not keep. It blocks for the slide
	// duration (pause-aware) and polls its own speed channel each cycle so a speed
	// change mid-slide takes effect immediately rather than waiting for the slide
	// to finish (the slide runs its own blocking loop separate from this node's
	// main loop). nil on test builds without injection — the caller then falls
	// back to the instant refill. beads is the OLD backup contents that become the
	// new working row.
	EmitRefillSlide func(clk Wiring.Clock, speedCh <-chan float64, beads []int)
	// Clock is this node's OWN clock storage, seeded by Wiring.reflectBuild
	// directly from the loader's origin (not derived from any specific wired
	// output port — deriving it from ToHoldNewSendOld/ToExcitatory/ToPacer was
	// fragile: whichever port happened to be wired first controlled pacing, and
	// per-goroutine-clock.md's API demolition removed port-derived clocks
	// entirely anyway). reflectBuild injects by matching struct fields typed
	// exactly `Wiring.Clock` (builders.go reflectBuild) — a bare field like this
	// is an unguarded nil-interface trap on any construction path reflectBuild
	// doesn't reach (a type rename that silently drops the injection, or a test
	// building &Node{} directly): an unguarded `clk.Tick()` panics with no
	// recover over the node goroutine, taking down every other node and the
	// buffer stream with it. Defaulted to Wiring.NewRealClock() by the Register
	// factory below so it is NEVER nil even before reflectBuild runs (or on a
	// test build with no loader); clock() re-guards on every read as a second
	// line of defense in case some future construction path bypasses the
	// factory. Production reflectBuild always overwrites this with the real
	// origin clock.
	Clock Wiring.Clock
	// SpeedCh delivers a speed change to THIS goroutine's own clk copy
	// (per-goroutine-clock.md "Delivery"), seeded by Wiring.reflectBuild
	// (injectSpeedChans) with a fresh buffered-1 channel. nil on a test build
	// with no loader — ApplySpeedNonBlocking is then always a no-op.
	SpeedCh          <-chan float64
	Init             []int `wire:"data.init"`
	Repeat           bool  `wire:"data.repeat"`
	ToHoldNewSendOld *Wiring.Out
	// ToExcitatory fans the emitted value out to a Pulse node (sample-and-hold). It is
	// optional: when unwired (Wired()==false) the emit is skipped so existing
	// topologies without a Pulse are unaffected.
	ToExcitatory *Wiring.Out
	// ToPacer fans the emitted value out to a Pacer node (sample-and-hold,
	// change-step feedback). Optional: when unwired (Wired()==false) the emit
	// is skipped so existing topologies without a Pacer are unaffected.
	ToPacer    *Wiring.Out
	FeedbackIn *Wiring.In
}

// clock returns n.Clock, guarded against nil (belt-and-suspenders: the
// Register factory below already seeds a real default, but this is the single
// read path every call site goes through so no future construction path can
// reintroduce the bare-nil panic hazard described on the Clock field).
func (n *Node) clock() Wiring.Clock {
	if n.Clock == nil {
		return Wiring.NewRealClock()
	}
	return n.Clock
}

// fanOutPlace places v on every wired fan-out output (same cycle — preserves
// concurrent fan-out) without driving them. Returns false only on a
// structural, TERMINAL failure (DriveItem.Failed() — a nil Out), mirroring
// EmitOneDriven's false-return-stops-the-goroutine convention. A momentarily
// full paced-wire buffer (DriveItem.BufferFull()) is TRANSIENT — the wire's
// own goroutine drains it every cycle — so it must NOT stop this node's
// goroutine; that bead is simply dropped from this cycle's fan-out (a
// breadcrumb was already emitted by PacedWire.Send) and the next Fire cycle
// tries again. Delivery is timed by each wire's own goroutine — this node no
// longer pins or steps a tick.
func (n *Node) fanOutPlace(v int) bool {
	if n.ToHoldNewSendOld.Wired() && n.ToHoldNewSendOld.PlaceDrivenAt(v).Failed() {
		return false
	}
	if n.ToExcitatory.Wired() && n.ToExcitatory.PlaceDrivenAt(v).Failed() {
		return false
	}
	if n.ToPacer.Wired() && n.ToPacer.PlaceDrivenAt(v).Failed() {
		return false
	}
	return true
}

// popEnd reads and removes the END element of working, refilling from backup
// when working empties. working/backup are the double-buffer: each is a fresh
// copy of init, and end-popping [1,0] yields 0 then 1. Returns the popped value.
// Caller guarantees len(working) > 0 (refill keeps it non-empty when init != nil).
func popEnd(working, backup *[]int, init []int) int {
	v := (*working)[len(*working)-1]
	*working = (*working)[:len(*working)-1]
	if len(*working) == 0 {
		// Refill: the top row (backup) slides down to become the new working
		// row; a fresh top row appears.
		*working = *backup
		*backup = append([]int(nil), init...)
	}
	return v
}

// updateFeedbackRing runs the feedback-ring emit path. It returns when ctx is
// cancelled or FeedbackIn closes. Called only when FeedbackIn.Wired() is true.
//
// Feedback ring: PEEK+SEND then READ. Sending does NOT deplete the buffer —
// each iteration peeks the END of working and launches that bead; the buffer
// stays full (4) at rest. The FIRST send is just the normal loop body
// (peek+send) running before any feedback is read, so the ring self-starts
// with no special seed and no t=0 deadlock.
//
// After sending, READ node 2's feedback s on FeedbackIn:
//
//	s == 1 -> POP the end (the "change the bead" action); refill on empty.
//	s == 0 -> hold: do nothing, keep sending the same last bead next loop.
func (n *Node) updateFeedbackRing(ctx context.Context, working, backup *[]int, init []int, emitBeads func(), clk Wiring.Clock) {
	// clk is this goroutine's own copy, taken once by the caller (Update) at
	// startup — docs/planning/visual-editor/per-goroutine-clock.md. Do not
	// re-derive from n.clock() here; that would be a second, independent copy
	// from the same shared source, defeating "one copy per goroutine".

	// ONE flat loop, identical in shape to the plain source path below: each
	// cycle does exactly one step of work, with NO nested wait loop. The
	// "waiting for node 2's feedback step" that used to be an inner loop is now
	// the flat `awaiting` flag carried across cycles: when false we peek+send a
	// fresh bead and arm the wait; when true we are mid-traversal and just
	// step+poll. Layout/drag handling is NOT here — it lives in the node's
	// dedicated always-on layout goroutine (split-layout-bead-goroutines.md), so
	// this bead loop is purely the pausable half and dragging is unaffected by
	// whatever this loop is waiting on.
	awaiting := false
	for {
		if ctx.Err() != nil {
			return
		}

		if !awaiting {
			// Guard: never peek an empty slice. Refill keeps working non-empty,
			// but be safe.
			if len(*working) == 0 {
				*working = *backup
				*backup = append([]int(nil), init...)
				emitBeads()
			}

			// PEEK the end (do NOT reslice) and SEND. Buffer unchanged. Node 1
			// places the same bead on every wired output the same cycle
			// (fanOutPlace — preserves concurrent fan-out) so node 2
			// (ToHoldNewSendOld) and node 6 (ToExcitatory) traverse in lockstep.
			v := (*working)[len(*working)-1]
			if n.Fire != nil {
				n.Fire()
			}
			if !n.fanOutPlace(v) {
				return
			}
			awaiting = true
		}

		// One step per cycle: sleep and poll FeedbackIn non-blocking. Each
		// fan-out wire's own goroutine advances its in-flight beads; this node
		// is never parked across the traversal and no longer steps the wires
		// itself.
		Wiring.ApplySpeedNonBlocking(clk, n.SpeedCh)
		if err := clk.SleepCycle(ctx); err != nil {
			return
		}

		s, ok := n.FeedbackIn.PollRecv()
		if !ok {
			// HoldNewSendOld's step has not arrived yet — keep cycling (drains
			// Layout again next pass). Still awaiting.
			continue
		}
		// Feedback arrived: re-arm the next peek+send regardless of hold/pop.
		awaiting = false
		if s != 1 {
			// Hold: buffer unchanged, send the same last bead next cycle.
			continue
		}

		// s == 1: POP the end (change the bead); refill when working empties.
		*working = (*working)[:len(*working)-1]
		if len(*working) == 0 {
			// Animated refill: the top row (backup) SLIDES DOWN into the
			// working row at human speed (clock-paced, pause-aware). After the
			// slide lands, the new top row appears via the full emitBeads below.
			if n.EmitRefillSlide != nil {
				n.EmitRefillSlide(clk, n.SpeedCh, *backup)
			}
			*working = *backup
			*backup = append([]int(nil), init...)
		}
		emitBeads() // array changed (pop, maybe refill) → restream interior
	}
}

func (n *Node) Update(ctx context.Context) {
	Wiring.TryEmit(n.EmitGeometry)
	if len(n.Init) == 0 {
		return
	}

	// Double-buffer derived from the spec init: working (bottom row) and backup
	// (top row), each a fresh copy of init. The working array IS the emission
	// state — no persistent index. End-popping is the read: end of working is
	// the next value out.
	init := append([]int(nil), n.Init...)
	working := append([]int(nil), init...)
	backup := append([]int(nil), init...)

	// emitBeads streams the live interior buffer as a discrete node-bead snapshot
	// (present beads only). Called on the initial full state and after every array
	// mutation (each pop, each refill) so the emitted set tracks working/backup.
	emitBeads := func() {
		if n.EmitNodeBeads != nil {
			n.EmitNodeBeads(working, backup)
		}
	}
	emitBeads() // initial full(4) state

	// Copy taken ONCE at this goroutine's start (Update IS the goroutine, run
	// once per Input node) — docs/planning/visual-editor/per-goroutine-clock.md.
	// Passed down to both branches below instead of each independently calling
	// n.clock() again.
	clk := n.clock().Copy()

	if n.FeedbackIn.Wired() {
		n.updateFeedbackRing(ctx, &working, &backup, init, emitBeads, clk)
		return
	}

	// Plain emit path (FeedbackIn not wired): Input is a periodic SOURCE. It pops
	// the end and fans the value to every wired output (2 and 3), then sleeps ONE
	// CADENCE — a sleep timer of (one human cycle) × (the fan-out edge length) —
	// before firing the next value. The bead is stepped one position per human
	// cycle DURING that sleep, so it traverses the edge across the cadence; with
	// equal-length output edges (assumed) both outputs stay in lockstep. With
	// Repeat the buffer refills forever; without it, once the working buffer is
	// drained it simply idles (no fire) but keeps cycling. Layout/drag handling
	// is NOT here — the node's dedicated always-on layout goroutine owns it
	// (split-layout-bead-goroutines.md), independent of this pausable bead loop.
	// clk is the same copy taken once above; no second derivation.
	emitted := 0
	// Fire cadence is measured in CLOCK TICKS, exactly like a gate's window/dwell
	// (gatecommon/gate.go: fire when now()-dwellStart >= fireDwellTicks). Tick()
	// freezes on Halt, so the cadence — and therefore emission — freezes on pause
	// just like every other node kind. The multiplication factor is the only
	// Input-specific part: the cadence is one tick per unit of the fan-out edge
	// length, recomputed each pass so a drag re-paces it.
	lastFireTick := clk.Tick() - int64(inputCadenceTicks(n)) // fire on the first pass
	for {
		if ctx.Err() != nil {
			return
		}
		now := clk.Tick()
		if (n.Repeat || emitted < len(init)) && now-lastFireTick >= int64(inputCadenceTicks(n)) {
			if n.Fire != nil {
				n.Fire()
			}
			v := popEnd(&working, &backup, init)
			emitBeads() // array changed (pop, maybe refill) → restream interior
			if !n.fanOutPlace(v) {
				return
			}
			lastFireTick = now
			emitted++
		}

		Wiring.ApplySpeedNonBlocking(clk, n.SpeedCh)
		if err := clk.SleepCycle(ctx); err != nil {
			return
		}
	}
}

// inputCadenceTicks is Input's fire cadence in clock ticks: the CROSSING TIME of
// the primary fan-out edge, ArcLength / PulseSpeedWuPerTick (= ticksToCross), so
// exactly one bead crosses the edge per cadence — no overlap. Measured in ticks,
// so it freezes on pause with Tick(). Recomputed live so a drag that changes the
// edge length re-paces emission.
func inputCadenceTicks(n *Node) int64 {
	c := int64(n.ToHoldNewSendOld.Geom().ArcLength / Wiring.PulseSpeedWuPerTick)
	if c < 1 {
		return 1
	}
	return c
}

func init() {
	// Seed Clock to a real, live-ticking default (never nil) at construction, so
	// it is safe even before reflectBuild's field-type injection runs (or on a
	// test build that registers/builds this kind with no loader at all).
	Wiring.Register("Input", func() any { return &Node{Clock: Wiring.NewRealClock()} })
}
