package hold

import (
	"context"

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
	// Layout is the hidden-layout-graph port (nodes/Wiring/layout_edge.go),
	// injected by the loader the same way EmitGeometry is. nil on builds
	// without a loader; Update nil-guards its poll.
	Layout *Wiring.LayoutPort
	In     *Wiring.In
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

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if p := h.Layout; p != nil {
			if msg, ok := p.TryRecv(); ok {
				p.Handle(msg)
			}
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
