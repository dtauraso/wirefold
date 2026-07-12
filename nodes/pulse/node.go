package pulse

import (
	"context"
	"sync/atomic"

	"github.com/dtauraso/wirefold/nodes/Wiring"
	"github.com/dtauraso/wirefold/nodes/gatecommon"
)

// Node is a sample-and-hold pulse. It HOLDS one int value (the thing it is
// outputting), initialized to noValue, and drives that held value out continuously.
// Even before any input arrives it emits noValue. When an input value arrives on
// FromInput, it UPDATES the held value; subsequent outputs emit the new value.
//
// Two goroutines split the two concerns so the held value (and its interior
// bead) updates when input arrives, with no one-output-drive lag:
//   - The MAIN loop runs one activity cycle per human clock tick: it does a
//     non-blocking input check (PollRecv), and on a new value emits the new
//     held-bead and stores the new held, then sleeps one human clock cycle
//     (clk.SleepCycle) — parking the CPU instead of spinning while idle.
//   - A DRIVE goroutine continuously pulses the CURRENT held value to the
//     output via gatecommon.DriveHeld (PlaceDriven + per-cycle StepOnce,
//     sleeping one cycle between steps), so this goroutine self-paces at the
//     wire rate and re-reads held each pulse — when held changes the next
//     pulse carries the new value.
//
// held is shared via sync/atomic so the two goroutines don't race.
//
// The output is NOT precondition-gated: Pulse self-emits noValue from the start
// (like the Input bootstrap), it is not inert until fed.
type Node struct {
	Fire         func()
	EmitGeometry func()
	// EmitHeldBead, injected by Wiring.reflectBuild, streams the held value as a
	// SINGLE centered interior node-bead (present when held != noValue). Re-emitted at
	// startup (held = noValue, empty interior) and whenever the held value changes.
	EmitHeldBead func(held int)
	FromInput    *Wiring.In
	Out          *Wiring.Out
	// Out2 is an optional SECOND continuous output driving the same held value, so a
	// Pulse can fan to two destinations (e.g. node 6 → node 5 via Out and → node 11
	// via Out2). Optional: when unwired (Wired()==false, e.g. node 7) its drive
	// goroutine is skipped, so single-output Pulse nodes are unaffected.
	Out2 *Wiring.Out
	// ToInput is a declared output back to an Input node. Intentionally
	// inert (no send logic) — see 3To1 edge task.
	ToInput *Wiring.Out
	// ToHoldNewSendOld is a declared output to a HoldNewSendOld node.
	// Intentionally inert (no send logic) — see 5To2/6To2 edge task.
	ToHoldNewSendOld *Wiring.Out
	// FromLeftGate is a declared input from a WindowAndInhibitLeftGate node.
	// Intentionally inert (no read logic) — see 9To3/9To6 edge task.
	FromLeftGate *Wiring.In
	// FromRightGate is a declared input from a WindowAndInhibitRightGate node.
	// Intentionally inert (no read logic) — see 10To6/10To8 edge task.
	FromRightGate *Wiring.In
	// Layout is the hidden-layout-graph port (nodes/Wiring/layout_edge.go),
	// injected by the loader the same way EmitGeometry is. nil on builds
	// without a loader; Update nil-guards its poll.
	Layout *Wiring.LayoutPort
}

// driveOutput runs a continuous-drive goroutine on out, always emitting the
// current value of held. Delegates to gatecommon.DriveHeld (shared with
// HoldFlip's identical-shaped drive goroutine) with an identity transform.
func driveOutput(ctx context.Context, out *Wiring.Out, held *atomic.Int64) {
	gatecommon.DriveHeld(ctx, out, held, func(h int64) int { return int(h) })
}

func (g *Node) Update(ctx context.Context) {
	Wiring.TryEmit(g.EmitGeometry)

	// held is shared between the drive goroutine(s) and this main loop.
	var held atomic.Int64
	held.Store(gatecommon.NoValue)
	if g.EmitHeldBead != nil {
		g.EmitHeldBead(gatecommon.NoValue) // startup: empty interior
	}

	// DRIVE goroutine: continuously pulse the current held value to Out.
	driveOutput(ctx, g.Out, &held)

	// Optional SECOND drive goroutine for Out2.
	if g.Out2 != nil && g.Out2.Wired() {
		driveOutput(ctx, g.Out2, &held)
	}

	// MAIN loop frame: do activities (non-blocking input check + update held),
	// then sleep one human clock cycle, repeat. The drive goroutine picks up the
	// new held on its next pulse. Sleeping one cycle per iteration (paced mode)
	// keeps the loop off the CPU 99% of the time instead of spinning millions of
	// times per human tick while there is nothing to receive.
	consume := func() {
		v, ok := g.FromInput.PollRecv()
		if !ok {
			return
		}
		if g.Fire != nil {
			g.Fire()
		}
		if int64(v) != held.Load() && g.EmitHeldBead != nil {
			g.EmitHeldBead(v) // show the new interior bead IMMEDIATELY
		}
		held.Store(int64(v))
	}

	clk := g.FromInput.Clock()

	// Paced mode: do activities, sleep one human clock cycle, repeat.
	for {
		if ctx.Err() != nil {
			return
		}
		consume()
		if err := clk.SleepCycle(ctx); err != nil {
			return
		}
	}
}

func init() {
	Wiring.Register("Pulse", func() any { return &Node{} })
}
