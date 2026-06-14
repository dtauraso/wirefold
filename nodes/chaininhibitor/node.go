package chaininhibitor

import (
	"context"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

type Node struct {
	Fire                       func()
	EmitGeometry               func()
	EmitHeldBead               func(held int)
	Held                       int `wire:"data.state"`
	FromPrevChainInhibitorNode *Wiring.In
	ToNext                     Wiring.OutMulti
	FeedbackOut                *Wiring.Out
}

// fanOutHeld forwards the held value concurrently on every ToNext output.
// Invariant: -1 (the empty-Held sentinel) is never sent on an output channel —
// a fire whose Held is -1 emits nothing on ToNext. Only the SEND is suppressed;
// Held still updates to the received value in the caller.
func fanOutHeld(ctx context.Context, outs Wiring.OutMulti, held int) {
	if held == -1 {
		return
	}
	outs.EmitManyDriven(ctx, held)
}

func (in *Node) Update(ctx context.Context) {
	if in.EmitGeometry != nil {
		in.EmitGeometry()
	}
	// Initialize the compare value for feedback detection.
	// -1 is the sentinel meaning "no value seen yet"; real values are non-negative
	// indices, so -1 never collides with a legitimate Init index.
	held := -1
	// Emit the initial interior bead state: held == -1 → present=false (empty
	// interior). The bead is re-emitted only when held actually changes below.
	if in.EmitHeldBead != nil {
		in.EmitHeldBead(held)
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Hold: if any output wire still has a bead in flight or holding an
		// unconsumed pulse, park until it clears — do not consume the input
		// pulse yet. This prevents drops when output transit time exceeds the
		// loop's input rate.
		anyOccupied := false
		for _, out := range in.ToNext {
			if out.Occupied() {
				anyOccupied = true
				break
			}
		}
		if anyOccupied {
			continue
		}

		if value, ok := in.FromPrevChainInhibitorNode.TryRecv(); ok {
			in.Fire()
			in.FromPrevChainInhibitorNode.Done()

			// Interior held-value bead: emit only when the held value changes
			// (-1 → 0 → 1 → 0 …). `held` is the running compare value tracking the
			// received value; the wired-feedback branch below also reads it for the
			// step computation, so update it once here at recv time.
			heldChanged := value != held
			held = value
			if heldChanged && in.EmitHeldBead != nil {
				in.EmitHeldBead(value)
			}

			if in.FeedbackOut.Wired() {
				// Place the feedback step BEFORE forwarding on ToNext. We only
				// order the PLACEMENT of the feedback bead relative to the
				// ToNext fan-out; we do NOT wait for the Input node to consume
				// it. FeedbackOut is fire-and-forget per MODEL.md ("does not
				// wait on the destination — no acknowledgment, no back-pressure"):
				// the node paces naturally on its next paced TryRecv, not on the
				// feedback round-trip, so the held value reaches ToNext at
				// fire-time instead of behind the ~feedback-traversal latency.
				// Step is 1 when the value changes (advance index), 0 when it
				// repeats (hold index). held == -1 on the first recv so the
				// first value always counts as a change.
				var step int
				if heldChanged {
					step = 1
				}
				in.FeedbackOut.EmitOneDriven(ctx, step)
				// Forward the current held value on the downstream chain.
				fanOutHeld(ctx, in.ToNext, in.Held)
				in.Held = value
			} else {
				// FeedbackOut not wired (e.g. i1): existing behavior unchanged.
				fanOutHeld(ctx, in.ToNext, in.Held)
				in.Held = value
			}
		}
	}
}

func init() {
	Wiring.Register("ChainInhibitor", func() any { return &Node{} })
}
