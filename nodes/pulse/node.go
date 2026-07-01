package pulse

import (
	"context"
	"sync/atomic"

	"github.com/dtauraso/wirefold/nodes/Wiring"
	"github.com/dtauraso/wirefold/nodes/gatecommon"
)

// noValue is the sentinel meaning "no value held yet". Real values are
// non-negative indices so noValue (-1) never collides with a legitimate value.
const noValue = gatecommon.NoValue

// Node is a sample-and-hold pulse. It HOLDS one int value (the thing it is
// outputting), initialized to noValue, and drives that held value out continuously.
// Even before any input arrives it emits noValue. When an input value arrives on
// FromInput, it UPDATES the held value; subsequent outputs emit the new value.
//
// Two goroutines split the two concerns so the held value (and its interior
// bead) updates the INSTANT input arrives, with no one-output-drive lag:
//   - The MAIN loop BLOCKS on input receive (TryRecv, which parks in paced mode
//     until a value is placed). The moment input arrives it emits the new
//     held-bead and stores the new held — exactly like HoldNewSendOld, so the
//     bead shows immediately.
//   - A DRIVE goroutine continuously pulses the CURRENT held value to the
//     output. EmitOneDriven is synchronous (blocks for the wire traversal), so
//     this goroutine self-paces at the wire rate and re-reads held each pulse —
//     when held changes the next pulse carries the new value.
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
}

// driveOutput runs a continuous-drive goroutine on out, always emitting the
// current value of held. Stops when ctx is cancelled or EmitOneDriven returns false.
func driveOutput(ctx context.Context, out *Wiring.Out, held *atomic.Int64) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			if !out.EmitOneDriven(ctx, int(held.Load())) {
				return
			}
		}
	}()
}

func (g *Node) Update(ctx context.Context) {
	Wiring.TryEmit(g.EmitGeometry)

	// held is shared between the drive goroutine(s) and this main loop.
	var held atomic.Int64
	held.Store(noValue)
	if g.EmitHeldBead != nil {
		g.EmitHeldBead(noValue) // startup: empty interior
	}

	// DRIVE goroutine: continuously pulse the current held value to Out.
	driveOutput(ctx, g.Out, &held)

	// Optional SECOND drive goroutine for Out2.
	if g.Out2 != nil && g.Out2.Wired() {
		driveOutput(ctx, g.Out2, &held)
	}

	// MAIN loop: BLOCK on input. The instant a value arrives, show the bead and
	// update held — the drive goroutine picks up the new held on its next pulse.
	for {
		v, ok := g.FromInput.TryRecv()
		if !ok {
			return // ctx cancelled or input closed
		}
		if g.Fire != nil {
			g.Fire()
		}
		if int64(v) != held.Load() && g.EmitHeldBead != nil {
			g.EmitHeldBead(v) // show the new interior bead IMMEDIATELY
		}
		held.Store(int64(v))
	}
}

func init() {
	Wiring.Register("Pulse", func() any { return &Node{} })
}
