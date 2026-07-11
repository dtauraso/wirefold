package hold

import (
	"context"
	"runtime"

	"github.com/dtauraso/wirefold/nodes/Wiring"
	"github.com/dtauraso/wirefold/nodes/gatecommon"
)

// noValue is the sentinel meaning "no value seen yet" → empty interior.
// Real values are non-negative indices so noValue (-1) never collides.
const noValue = gatecommon.NoValue

// Node is a terminal "Hold" kind: it receives a value on its single input,
// holds/displays it, and produces NO output. On each received value it fires,
// updates Held, and re-emits the held bead when the value changes.
type Node struct {
	Fire         func()
	EmitGeometry func()
	EmitHeldBead func(held int)
	Held         int `wire:"data.state"`
	In           *Wiring.In
	// ToHoldNewSendOld is a declared output to a HoldNewSendOld node.
	// Intentionally inert (no send logic) — see 7To5 edge task.
	ToHoldNewSendOld *Wiring.Out
}

func (h *Node) Update(ctx context.Context) {
	Wiring.TryEmit(h.EmitGeometry)

	held := noValue
	if h.EmitHeldBead != nil {
		h.EmitHeldBead(held)
	}

	clk := h.In.Clock()
	if clk == nil {
		// chan mode (tests without a paced clock): keep the busy-poll fallback.
		for {
			select {
			case <-ctx.Done():
				return
			default:
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
			} else {
				runtime.Gosched()
			}
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := clk.WaitTick(ctx, clk.Tick()+1); err != nil {
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
	Wiring.Register("Hold", func() any { return &Node{} })
}
