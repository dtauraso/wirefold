package input

import (
	"context"
	"runtime"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

type Node struct {
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
	// Layout is the hidden-layout-graph port (nodes/Wiring/layout_edge.go),
	// injected by the loader the same way EmitGeometry is. nil on builds
	// without a loader; Update nil-guards its poll.
	Layout           *Wiring.LayoutPort
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
	// FromHoldNewSendOld is a declared feedback input from node 2's ToInput
	// output. Intentionally inert (no read logic) — see 2To1 edge task.
	FromHoldNewSendOld *Wiring.In
	// FromPulse is a declared feedback input from a Pulse node's ToInput
	// output. Intentionally inert (no read logic) — see 3To1 edge task.
	FromPulse *Wiring.In
}

// fanOut places beads on all wired outputs and drives them concurrently via
// DriveAll, so every traversal animates in lockstep on this goroutine. Used
// only in the nil-clock (chan mode / unit test) fallback — the paced path
// below places+steps each output non-blocking instead.
func (n *Node) fanOut(ctx context.Context, v int) {
	items := []Wiring.DriveItem{n.ToHoldNewSendOld.PlaceDriven(v)}
	if n.ToExcitatory.Wired() {
		items = append(items, n.ToExcitatory.PlaceDriven(v))
	}
	if n.ToPacer.Wired() {
		items = append(items, n.ToPacer.PlaceDriven(v))
	}
	Wiring.DriveAll(ctx, items)
}

// fanOutInFlight reports whether ANY wired fan-out output still has a bead
// traversing its wire. Gates firing the next value: a new value is placed
// only once every prior fan-out bead has been fully delivered, matching the
// old blocking fanOut/DriveAll cadence (one pop per full traversal).
func (n *Node) fanOutInFlight() bool {
	if n.ToHoldNewSendOld.InFlight() {
		return true
	}
	if n.ToExcitatory.Wired() && n.ToExcitatory.InFlight() {
		return true
	}
	if n.ToPacer.Wired() && n.ToPacer.InFlight() {
		return true
	}
	return false
}

// fanOutPlace places v on every wired fan-out output (same cycle — preserves
// concurrent fan-out) without driving them. Returns false if any wired
// placement failed (faded/torn-down wire), mirroring EmitOneDriven's
// false-return-stops-the-goroutine convention.
func (n *Node) fanOutPlace(v int) bool {
	if !n.ToHoldNewSendOld.PlaceDriven(v).Live() {
		return false
	}
	if n.ToExcitatory.Wired() && !n.ToExcitatory.PlaceDriven(v).Live() {
		return false
	}
	if n.ToPacer.Wired() && !n.ToPacer.PlaceDriven(v).Live() {
		return false
	}
	return true
}

// fanOutStepOnce advances every wired fan-out output by one non-blocking
// tick-step. Called once per WaitTick cycle so all fan-out beads advance
// together in lockstep, one step per cycle — never a nested pump.
func (n *Node) fanOutStepOnce(ctx context.Context) {
	n.ToHoldNewSendOld.StepOnce(ctx)
	if n.ToExcitatory.Wired() {
		n.ToExcitatory.StepOnce(ctx)
	}
	if n.ToPacer.Wired() {
		n.ToPacer.StepOnce(ctx)
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
	clk := n.ToHoldNewSendOld.Clock()
	if clk == nil {
		// chan mode (tests without a paced clock): keep the original blocking
		// fanOut/DriveAll behavior.
		for {
			if ctx.Err() != nil {
				return
			}

			if len(*working) == 0 {
				*working = *backup
				*backup = append([]int(nil), init...)
				emitBeads()
			}

			v := (*working)[len(*working)-1]
			if n.Fire != nil {
				n.Fire()
			}
			n.fanOut(ctx, v)

			step, ok := n.FeedbackIn.PollRecv()
			for !ok {
				if ctx.Err() != nil {
					return
				}
				runtime.Gosched()
				step, ok = n.FeedbackIn.PollRecv()
			}
			if step != 1 {
				continue
			}

			*working = (*working)[:len(*working)-1]
			if len(*working) == 0 {
				if n.EmitRefillSlide != nil {
					n.EmitRefillSlide(*backup)
				}
				*working = *backup
				*backup = append([]int(nil), init...)
			}
			emitBeads()
		}
	}

	for {
		if ctx.Err() != nil {
			return
		}

		if p := n.Layout; p != nil {
			if msg, ok := p.TryRecv(); ok {
				p.Handle(msg)
			}
		}

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

		// Single loop, one step per cycle: WaitTick, StepOnce every fan-out
		// output, and poll FeedbackIn non-blocking, until HoldNewSendOld's
		// step arrives. This node is never parked across the traversal — it
		// returns to the top of this loop and WaitTicks one cycle at a time
		// (same shape as pacer/gatecommon.DriveHeld), not a nested
		// InFlight-pump.
		var step int
		for {
			if ctx.Err() != nil {
				return
			}
			if err := clk.WaitTick(ctx, clk.Tick()+1); err != nil {
				return
			}
			n.fanOutStepOnce(ctx)
			if s, ok := n.FeedbackIn.PollRecv(); ok {
				step = s
				break
			}
		}
		if step != 1 {
			// Hold: buffer unchanged, send the same last bead next loop.
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

	// Plain emit path (FeedbackIn not wired): pop the end every iteration,
	// refilling on empty. With Repeat the buffer refills forever; without it,
	// emit exactly len(init) values (one working drain) then stop.
	clk := n.ToHoldNewSendOld.Clock()
	if clk == nil {
		// chan mode (tests without a paced clock): keep the original blocking
		// fanOut/DriveAll behavior.
		emitted := 0
		for n.Repeat || emitted < len(init) {
			if ctx.Err() != nil {
				return
			}
			if p := n.Layout; p != nil {
				if msg, ok := p.TryRecv(); ok {
					p.Handle(msg)
				}
			}
			if n.Fire != nil {
				n.Fire()
			}
			v := popEnd(&working, &backup, init)
			emitBeads()
			n.fanOut(ctx, v)
			emitted++
		}
		return
	}

	// Single loop, one step per cycle: on fire, PLACE the bead on every
	// wired fan-out output (place-all-then-drive — preserves concurrent
	// fan-out, same cycle); every cycle StepOnce each fan-out output once.
	// The next value is only popped once every prior fan-out bead has been
	// fully delivered (fanOutInFlight), matching the old blocking fanOut
	// cadence of one pop per full traversal. When there is nothing left to
	// fire (Repeat false and drained) the loop keeps stepping until the last
	// fan-out bead is delivered, then returns — it never abandons an
	// in-flight bead.
	emitted := 0
	for {
		if ctx.Err() != nil {
			return
		}
		if p := n.Layout; p != nil {
			if msg, ok := p.TryRecv(); ok {
				p.Handle(msg)
			}
		}
		stillToFire := n.Repeat || emitted < len(init)
		inFlight := n.fanOutInFlight()
		if !stillToFire && !inFlight {
			return
		}
		if stillToFire && !inFlight {
			if n.Fire != nil {
				n.Fire()
			}
			v := popEnd(&working, &backup, init)
			emitBeads() // array changed (pop, maybe refill) → restream interior
			if !n.fanOutPlace(v) {
				return
			}
			emitted++
		}
		if err := clk.WaitTick(ctx, clk.Tick()+1); err != nil {
			return
		}
		n.fanOutStepOnce(ctx)
	}
}

func init() {
	Wiring.Register("Input", func() any { return &Node{} })
}
