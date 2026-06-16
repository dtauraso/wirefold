package pacer

import (
	"context"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

type Node struct {
	Fire         func()
	EmitGeometry func()
	EmitHeldBead func(held int)
	Held         int `wire:"data.state"`
	FromInput    *Wiring.In
	FeedbackOut  *Wiring.Out
}

func (p *Node) Update(ctx context.Context) {
	if p.EmitGeometry != nil {
		p.EmitGeometry()
	}
	// -1 is the sentinel meaning "no value seen yet"; real values are
	// non-negative indices, so -1 never collides with a legitimate value.
	held := -1
	if p.EmitHeldBead != nil {
		p.EmitHeldBead(held)
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if value, ok := p.FromInput.TryRecv(); ok {
			p.Fire()
			p.FromInput.Done()

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
			items := []Wiring.DriveItem{p.FeedbackOut.PlaceDriven(step)}
			p.Held = value
			Wiring.DriveAll(ctx, items)
		}
	}
}

func init() {
	Wiring.Register("Pacer", func() any { return &Node{} })
}
