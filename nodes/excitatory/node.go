package excitatory

import (
	"context"
	"sync/atomic"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

// Node is a sample-and-hold excitatory. It HOLDS one int value (the thing it is
// outputting), initialized to -1, and drives that held value out continuously.
// Even before any input arrives it emits -1. When an input value arrives on
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
// held is shared via sync/atomic so the two goroutines don't race.
//
// The output is NOT precondition-gated: Excitatory self-emits -1 from the start
// (like the Input bootstrap), it is not inert until fed.
type Node struct {
	Fire         func()
	EmitGeometry func()
	// EmitHeldBead, injected by Wiring.reflectBuild, streams the held value as a
	// SINGLE centered interior node-bead (present when held != -1). Re-emitted at
	// startup (held = -1, empty interior) and whenever the held value changes.
	EmitHeldBead func(held int)
	FromInput    *Wiring.In
	Out          *Wiring.Out
}

func (g *Node) Update(ctx context.Context) {
	if g.EmitGeometry != nil {
		g.EmitGeometry()
	}

	// held is shared between the drive goroutine and this main loop.
	var held atomic.Int64
	held.Store(-1)
	if g.EmitHeldBead != nil {
		g.EmitHeldBead(-1) // startup: empty interior (held == -1)
	}

	// DRIVE goroutine: continuously pulse the current held value to node 5.
	// EmitOneDriven is synchronous (blocks for the full wire traversal), so this
	// self-paces at the wire rate. Reading held each iteration means the next
	// pulse after an input update carries the new value. Stops on ctx cancel.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			if !g.Out.EmitOneDriven(ctx, int(held.Load())) {
				return
			}
		}
	}()

	// MAIN loop: BLOCK on input. The instant a value arrives, show the bead and
	// update held — the drive goroutine picks up the new held on its next pulse.
	for {
		v, ok := g.FromInput.TryRecv()
		if !ok {
			return // ctx cancelled or input closed
		}
		g.FromInput.Done()
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
	Wiring.Register("Excitatory", func() any { return &Node{} })
}
