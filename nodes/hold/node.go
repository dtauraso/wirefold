package hold

import (
	"context"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

// noValue is the sentinel meaning "no value seen yet" → empty interior.
// Real values are non-negative indices so noValue (-1) never collides.
// Aliases Wiring.NoValue, the one definition (gatecommon.NoValue aliases the
// same constant).
const noValue = Wiring.NoValue

// Node is a terminal "Hold" kind: it receives a value on its single input,
// holds/displays it, and produces NO output. On each received value it fires,
// updates Held, and re-emits the held bead when the value changes.
type Node struct {
	Wiring.LayoutHolder
	Fire         func()
	EmitGeometry func()
	EmitHeldBead func(held int)
	Held         int `wire:"data.state"`
	In           *Wiring.In
}

func (h *Node) Update(ctx context.Context) {
	Wiring.TryEmit(h.EmitGeometry)

	held := noValue
	if h.EmitHeldBead != nil {
		h.EmitHeldBead(held)
	}

	clk := h.In.Clock()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := clk.SleepCycle(ctx); err != nil {
			return
		}

		if value, ok := h.In.PollRecv(); ok {
			if h.Fire != nil {
				h.Fire()
			}
			if value != held && h.EmitHeldBead != nil {
				h.EmitHeldBead(value)
			}
			held = value
			h.Held = value
		}
	}
}

func init() {
	// Held defaults to the empty sentinel, not the int zero-value (0 is a real
	// held value). See holdnewsendold for the seed rationale.
	Wiring.Register("Hold", func() any { return &Node{Held: noValue} })
}
