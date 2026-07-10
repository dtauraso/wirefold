package holdflip

import (
	"context"
	"sync/atomic"

	"github.com/dtauraso/wirefold/nodes/Wiring"
	"github.com/dtauraso/wirefold/nodes/gatecommon"
)

// Node is a drain-to-latest flip node. It HOLDS one int value (the last
// received input), initialized to noValue, and drives the FLIPPED value (1-held)
// out continuously.
//
// Two goroutines split the two concerns so the held value (and its interior
// bead) updates the INSTANT input arrives, with no one-output-drive lag:
//   - The MAIN loop BLOCKS on input receive (TryRecv, which parks in paced
//     mode until a value is placed), then drains any additional queued beads
//     non-blocking via PollRecv to keep only the LATEST value. It calls
//     g.In.Done(), g.Fire(), updates the atomic held, and emits the interior
//     bead when held changes.
//   - A DRIVE goroutine continuously pulses 1-held to the output.
//     EmitOneDriven is synchronous (blocks for the wire traversal), so this
//     self-paces at the wire rate and re-reads held each pulse — when held
//     changes the next pulse carries the flipped new value.
//
// held is shared via sync/atomic so the two goroutines don't race.
type Node struct {
	Fire         func()
	EmitGeometry func()
	// EmitHeldBead, injected by Wiring.reflectBuild, streams the held INPUT value
	// as a SINGLE centered interior node-bead (present when held != noValue).
	// Re-emitted at startup (held = noValue, empty interior) and whenever the held
	// value changes.
	EmitHeldBead func(held int)
	In           *Wiring.In
	Out          *Wiring.Out
	// Layout is the hidden-layout-graph port (nodes/Wiring/layout_edge.go),
	// injected by the loader the same way EmitGeometry is. nil on builds
	// without a loader; Update nil-guards its poll.
	Layout *Wiring.LayoutPort
}

func (g *Node) Update(ctx context.Context) {
	Wiring.TryEmit(g.EmitGeometry)

	// held is shared between the drive goroutine and this main loop.
	var held atomic.Int64
	held.Store(gatecommon.NoValue)
	if g.EmitHeldBead != nil {
		g.EmitHeldBead(gatecommon.NoValue) // startup: empty interior
	}

	// DRIVE goroutine: continuously pulse the FLIPPED current held value to Out.
	// Delegates to gatecommon.DriveHeld (shared with Pulse's identical-shaped
	// drive goroutine); EmitOneDriven is synchronous (blocks for the full wire
	// traversal), so this self-paces at the wire rate. Reading held each
	// iteration means the next pulse after an input update carries the new
	// flipped value. Stops on ctx cancel.
	gatecommon.DriveHeld(ctx, g.Out, &held, func(h int64) int {
		if h == gatecommon.NoValue {
			return gatecommon.NoValue // no value yet; emit sentinel so wire doesn't carry garbage
		}
		return 1 - int(h)
	})

	// MAIN loop: BLOCK on input (TryRecv parks in paced mode). Once a value
	// arrives, drain any additional queued beads non-blocking (PollRecv) to keep
	// only the LATEST. Then Done()/Fire()/update held/emit interior bead.
	var lastDisplayed int64 = gatecommon.NoValue
	for {
		if p := g.Layout; p != nil {
			if msg, ok := p.TryRecv(); ok {
				p.Handle(msg)
			}
		}
		v, ok := g.In.TryRecv()
		if !ok {
			return // ctx cancelled or input closed
		}
		// Drain-to-latest: consume any additional queued beads, keeping the last
		// REAL value. A stray NoValue sentinel must not overwrite v (storing -1 would
		// emit 1-(-1)=2) — mirrors gatecommon.drainLatestReal's NoValue guard.
		for {
			next, ok := g.In.PollRecv()
			if !ok {
				break
			}
			if next != gatecommon.NoValue {
				v = next
			}
		}
		if g.Fire != nil {
			g.Fire()
		}
		newHeld := int64(v)
		held.Store(newHeld)
		if newHeld != lastDisplayed && g.EmitHeldBead != nil {
			g.EmitHeldBead(v)
		}
		lastDisplayed = newHeld
	}
}

func init() {
	Wiring.Register("HoldFlip", func() any { return &Node{} })
}
