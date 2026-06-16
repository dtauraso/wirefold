package hold

import (
	"context"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

// Node is a terminal "Hold" kind: it receives a value on its single input,
// holds/displays it, and produces NO output. On each received value it fires,
// updates Held, and re-emits the held bead when the value changes.
type Node struct {
	Fire         func()
	EmitGeometry func()
	EmitHeldBead func(held int)
	Held         int `wire:"data.state"`
	In           *Wiring.In
}

func (h *Node) Update(ctx context.Context) {
	if h.EmitGeometry != nil {
		h.EmitGeometry()
	}

	// held tracks the last received value displayed inside the node sphere.
	// -1 is the sentinel meaning "no value seen yet" → empty interior.
	held := -1
	if h.EmitHeldBead != nil {
		h.EmitHeldBead(held)
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if value, ok := h.In.TryRecv(); ok {
			h.Fire()
			h.In.Done()
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
