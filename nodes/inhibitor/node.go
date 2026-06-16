package inhibitor

import (
	"context"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

type Node struct {
	Fire                       func()
	EmitGeometry               func()
	EmitHeldBead               func(held int)
	Held                       int `wire:"data.state"`
	FromPrevInhibitorNode *Wiring.In
	ToNext                     Wiring.OutMulti
	FeedbackOut                *Wiring.Out
}

// placeHeld appends the ToNext fan-out beads (held value) to items WITHOUT driving
// them, returning the extended set. Invariant: -1 (the empty-Held sentinel) is
// never sent on an output channel — a fire whose Held is -1 places nothing on
// ToNext. Only the SEND is suppressed; Held still updates to the received value in
// the caller. The caller drives these together with the feedback bead in ONE
// Wiring.DriveAll so every outbound bead animates concurrently and the node
// goroutine blocks once (for the fan-out flight) rather than once per edge.
func placeHeld(outs Wiring.OutMulti, held int, items []Wiring.DriveItem) []Wiring.DriveItem {
	if held == -1 {
		return items
	}
	return outs.PlaceDrivenAll(held, items)
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

		if value, ok := in.FromPrevInhibitorNode.TryRecv(); ok {
			in.Fire()
			in.FromPrevInhibitorNode.Done()

			// Interior held-value bead: emit only when the held value changes
			// (-1 → 0 → 1 → 0 …). `held` is the running compare value tracking the
			// received value; the wired-feedback branch below also reads it for the
			// step computation, so update it once here at recv time.
			heldChanged := value != held
			held = value
			if heldChanged && in.EmitHeldBead != nil {
				in.EmitHeldBead(value)
			}

			// Drive every outbound bead concurrently on THIS goroutine: place the
			// feedback step (when wired) and the ToNext fan-out beads WITHOUT
			// walkers, then drive them all together in one Wiring.DriveAll. This
			// is the key to throughput: a per-edge EmitOneDriven/EmitManyDriven
			// blocks the goroutine for each edge's full traversal in turn, so a
			// node with a long feedback edge and a long ToNext edge would block ~2×
			// the flight time per fire (the regression that stalled output). One
			// combined drive blocks ONCE for the longest edge and the beads animate
			// in parallel — matching the old walker concurrency with no walkers.
			//
			// Placement order: feedback is placed BEFORE the ToNext fan-out (we
			// order only the PLACEMENT, not consumption — FeedbackOut is
			// fire-and-forget per MODEL.md). Step is 1 when the value changes
			// (advance index), 0 when it repeats (hold index); held == -1 on the
			// first recv so the first value always counts as a change.
			var items []Wiring.DriveItem
			if in.FeedbackOut.Wired() {
				var step int
				if heldChanged {
					step = 1
				}
				items = append(items, in.FeedbackOut.PlaceDriven(step))
			}
			items = placeHeld(in.ToNext, in.Held, items)
			in.Held = value
			Wiring.DriveAll(ctx, items)
		}
	}
}

func init() {
	Wiring.Register("Inhibitor", func() any { return &Node{} })
}
