package pacer

import (
	"context"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

// noValue is the sentinel meaning "no value seen yet". Real values are
// non-negative indices so noValue (-1) never collides with a legitimate value.
// Aliases Wiring.NoValue, the one definition (gatecommon.NoValue aliases the
// same constant).
const noValue = Wiring.NoValue

type Node struct {
	Wiring.LayoutHolder
	Fire         func()
	EmitGeometry func()
	EmitHeldBead func(held int)
	Held         int `wire:"data.state"`
	FromInput    *Wiring.In
	FeedbackOut  *Wiring.Out
}

func (p *Node) Update(ctx context.Context) {
	Wiring.TryEmit(p.EmitGeometry)

	held := noValue
	if p.EmitHeldBead != nil {
		p.EmitHeldBead(held)
	}

	clk := p.FromInput.Clock()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := clk.SleepCycle(ctx); err != nil {
			return
		}

		if value, ok := p.FromInput.PollRecv(); ok {
			if p.Fire != nil {
				p.Fire()
			}

			heldChanged := value != held
			held = value
			if heldChanged && p.EmitHeldBead != nil {
				p.EmitHeldBead(value)
			}

			// Change-step feedback: 1 when the value changed (or first recv),
			// 0 when it repeats. Placed fire-and-forget on FeedbackOut (no
			// consume acknowledgment, per MODEL.md).
			step := 0
			if heldChanged {
				step = 1
			}
			p.Held = value

			p.FeedbackOut.PlaceDriven(step)
		}

		// Single loop, one step per cycle: advance any in-flight output bead
		// exactly one position-step. The node is never parked across a
		// traversal — it returns to the top and sleeps one cycle. (A new
		// input arriving mid-traversal is not a case; there is no place/step
		// collision to guard.)
		p.FeedbackOut.StepOnce(ctx)
	}
}

func init() {
	Wiring.Register("Pacer", func() any { return &Node{} })
}
