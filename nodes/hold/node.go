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
	// Clock is this node's OWN clock storage, seeded by Wiring.reflectBuild
	// directly from the loader's origin (bare-field injection by exact type
	// Wiring.Clock — see input.Node.Clock; ports no longer hand out a clock,
	// per-goroutine-clock.md API demolition item 1). Update() Copies it exactly
	// once at its own start.
	Clock Wiring.Clock
	// SpeedCh delivers a speed change to THIS goroutine's own clk copy
	// (per-goroutine-clock.md "Delivery"), seeded by Wiring.reflectBuild
	// (injectSpeedChans). nil on a test build with no loader.
	SpeedCh <-chan float64
	In      *Wiring.In
}

func (h *Node) Update(ctx context.Context) {
	Wiring.TryEmit(h.EmitGeometry)

	held := noValue
	if h.EmitHeldBead != nil {
		h.EmitHeldBead(held)
	}

	// Copy taken ONCE at this goroutine's start (Update IS the goroutine) —
	// docs/planning/visual-editor/per-goroutine-clock.md.
	clk := h.Clock.Copy()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		Wiring.ApplySpeedNonBlocking(clk, h.SpeedCh)
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
