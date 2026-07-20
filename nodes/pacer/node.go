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
	// Clock is this node's OWN clock storage, seeded by Wiring.reflectBuild
	// directly from the loader's origin (bare-field injection by exact type
	// Wiring.Clock — see input.Node.Clock; ports no longer hand out a clock,
	// per-goroutine-clock.md API demolition item 1). Update() Copies it exactly
	// once at its own start.
	Clock Wiring.Clock
	// SpeedCh delivers a speed change to THIS goroutine's own clk copy
	// (per-goroutine-clock.md "Delivery"), seeded by Wiring.reflectBuild
	// (injectSpeedChans). nil on a test build with no loader.
	SpeedCh     <-chan float64
	FromInput   *Wiring.In
	FeedbackOut *Wiring.Out
}

func (p *Node) Update(ctx context.Context) {
	Wiring.TryEmit(p.EmitGeometry)

	held := noValue
	if p.EmitHeldBead != nil {
		p.EmitHeldBead(held)
	}

	// Copy taken ONCE at this goroutine's start (Update IS the goroutine) —
	// docs/planning/visual-editor/per-goroutine-clock.md.
	clk := p.Clock.Copy()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		Wiring.ApplySpeedNonBlocking(clk, p.SpeedCh)
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

			p.FeedbackOut.PlaceDrivenAt(step, clk.Tick())
		}

		// Single loop, one step per cycle: advance any in-flight output bead
		// exactly one position-step. The node is never parked across a
		// traversal — it returns to the top and sleeps one cycle. (A new
		// input arriving mid-traversal is not a case; there is no place/step
		// collision to guard.)
		p.FeedbackOut.StepOnceAt(ctx, clk.Tick())
	}
}

func init() {
	// Held defaults to the empty sentinel, not the int zero-value (0 is a real
	// held value). See holdnewsendold for the seed rationale.
	Wiring.Register("Pacer", func() any { return &Node{Held: noValue} })
}
