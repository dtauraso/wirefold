package excitatory

import (
	"context"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

// Node is a sample-and-hold excitatory. It HOLDS one int value (the thing it is
// outputting), initialized to -1, and drives that held value out continuously.
// Even before any input arrives it emits -1. When an input value arrives on
// FromInput, it UPDATES the held value; subsequent outputs emit the new value.
//
// The loop is paced naturally by the synchronous driven emit (EmitOneDriven
// blocks until the bead is delivered). The output is NOT precondition-gated:
// Excitatory self-emits -1 from the start (like the Input bootstrap), it is not
// inert until fed.
type Node struct {
	Fire         func()
	EmitGeometry func()
	FromInput    *Wiring.In
	Out          *Wiring.Out
}

func (g *Node) Update(ctx context.Context) {
	if g.EmitGeometry != nil {
		g.EmitGeometry()
	}

	held := -1
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if v, ok := g.FromInput.PollRecv(); ok {
			g.FromInput.Done()
			held = v
		}

		if g.Fire != nil {
			g.Fire()
		}
		// Drive the held value to node 5. Blocks until delivered (paces the loop).
		if !g.Out.EmitOneDriven(ctx, held) {
			return
		}
	}
}

func init() {
	Wiring.Register("Excitatory", func() any { return &Node{} })
}
