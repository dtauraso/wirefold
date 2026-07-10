package pacer

import (
	"context"
	"runtime"

	"github.com/dtauraso/wirefold/nodes/Wiring"
	"github.com/dtauraso/wirefold/nodes/gatecommon"
)

// noValue is the sentinel meaning "no value seen yet". Real values are
// non-negative indices so noValue (-1) never collides with a legitimate value.
const noValue = gatecommon.NoValue

type Node struct {
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

	for {
		select {
		case <-ctx.Done():
			return
		default:
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
			items := []Wiring.DriveItem{p.FeedbackOut.PlaceDriven(step)}
			p.Held = value
			Wiring.DriveAll(ctx, items)
		} else {
			runtime.Gosched()
		}
	}
}

func init() {
	Wiring.Register("Pacer", func() any { return &Node{} })
}
