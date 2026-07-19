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
	// Wiring.reflectBuild (captures this node's id + geometry + the shared clock).
	// It blocks for the slide duration (pause-aware). nil on test builds without
	// injection — the caller then falls back to the instant refill. beads is the
	// OLD backup contents that become the new working row.
	EmitRefillSlide func(beads []int)
	// Clock is the shared node-level clock, injected by Wiring.reflectBuild
	// directly (not derived from any specific wired output port — deriving it
	// from ToHoldNewSendOld/ToExcitatory/ToPacer was fragile: whichever port
	// happened to be wired first controlled pacing). reflectBuild injects by
	// matching struct fields typed exactly `Wiring.Clock` (builders.go
	// reflectBuild) — a bare field like this is an unguarded nil-interface trap
	// on any construction path reflectBuild doesn't reach (a type rename that
	// silently drops the injection, or a test building &Node{} directly),
	// exactly the hazard ports.go's In.Clock() comment describes for
	// PORT-derived clocks: an unguarded `clk.Tick()` panics with no recover
	// over the node goroutine, taking down every other node and the buffer
	// stream with it. Defaulted to Wiring.NewInertClock() by the Register
	// factory below so it is NEVER nil even before reflectBuild runs (or on a
	// test build with no loader); clock() re-guards on every read as a second
	// line of defense in case some future construction path bypasses the
	// factory. Production reflectBuild always overwrites this with the real
	// shared clock.
	Clock            Wiring.Clock
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
		return Wiring.NewInertClock()
	}
	return n.Clock
}

// fanOutPlace places v on every wired fan-out output (same cycle — preserves
// concurrent fan-out) without driving them. Returns false if any wired
// placement failed (torn-down wire), mirroring EmitOneDriven's
// false-return-stops-the-goroutine convention.
//
// tick is snapshotted ONCE by the caller (clk.Tick(), read a single time)
// and passed to every wired output's PlaceDrivenAt so all fan-out beads
// stamp the SAME placementTick. Placing sequentially with each wire
// independently re-reading the live shared clock (PlaceDriven) lets the
// clock advance between placements — under a concurrently advancing clock the two
// equal-latency siblings can land on either side of a tick boundary and get
// different placementTicks, delivering a full cycle apart despite identical
// latency.
func (n *Node) fanOutPlace(v int, tick int64) bool {
	if n.ToHoldNewSendOld.Wired() && n.ToHoldNewSendOld.PlaceDrivenAt(v, tick).Failed() {
		return false
	}
	if n.ToExcitatory.Wired() && n.ToExcitatory.PlaceDrivenAt(v, tick).Failed() {
		return false
	}
	if n.ToPacer.Wired() && n.ToPacer.PlaceDrivenAt(v, tick).Failed() {
		return false
	}
	return true
}

// fanOutStepOnce advances every wired fan-out output by one non-blocking
// tick-step. Called once per WaitTick cycle so all fan-out beads advance
// together in lockstep, one step per cycle — never a nested pump. tick is
// the PINNED current tick (snapshotted once by the caller right after
// WaitTick) so every fan-out wire observes the same tick this cycle instead
// of each independently re-reading the shared clock.
func (n *Node) fanOutStepOnce(ctx context.Context, tick int64) {
	n.ToHoldNewSendOld.StepOnceAt(ctx, tick)
	if n.ToExcitatory.Wired() {
		n.ToExcitatory.StepOnceAt(ctx, tick)
	}
	if n.ToPacer.Wired() {
		n.ToPacer.StepOnceAt(ctx, tick)
	}
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
func (n *Node) updateFeedbackRing(ctx context.Context, working, backup *[]int, init []int, emitBeads func()) {
	clk := n.clock()

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
			if !n.fanOutPlace(v, clk.Tick()) {
				return
			}
			awaiting = true
		}

		// One step per cycle: sleep, StepOnce every fan-out output, and poll
		// FeedbackIn non-blocking. Same one-step-per-cycle cadence as
		// pacer/gatecommon.DriveHeld and the plain source path; the node is
		// never parked across the traversal.
		if err := clk.SleepCycle(ctx); err != nil {
			return
		}
		n.fanOutStepOnce(ctx, clk.Tick())

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
				n.EmitRefillSlide(*backup)
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

	if n.FeedbackIn.Wired() {
		n.updateFeedbackRing(ctx, &working, &backup, init, emitBeads)
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
	clk := n.clock()
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
			if !n.fanOutPlace(v, now) {
				return
			}
			lastFireTick = now
			emitted++
		}

		if err := clk.SleepCycle(ctx); err != nil {
			return
		}
		n.fanOutStepOnce(ctx, clk.Tick())
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
	// Seed Clock to the real inert default (never nil) at construction, so it
	// is safe even before reflectBuild's field-type injection runs (or on a
	// test build that registers/builds this kind with no loader at all).
	Wiring.Register("Input", func() any { return &Node{Clock: Wiring.NewInertClock()} })
}
