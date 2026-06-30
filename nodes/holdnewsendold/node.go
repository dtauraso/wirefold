package holdnewsendold

import (
	"context"
	"time"

	"github.com/dtauraso/wirefold/nodes/Wiring"
)

// noValue is the sentinel meaning "no value seen yet". Real values are
// non-negative indices so noValue (-1) never collides with a legitimate value.
const noValue = -1

// occupiedPollInterval is the park duration between output-occupied checks to
// avoid a busy-spin while waiting for the output wire to clear.
const occupiedPollInterval = time.Millisecond

type Node struct {
	Fire                       func()
	EmitGeometry               func()
	EmitHeldBead               func(held int)
	Held                       int `wire:"data.state"`
	FromPrevHoldNewSendOldNode *Wiring.In
	ToNext                     Wiring.OutMulti
}

func (in *Node) tryEmitGeometry() {
	if in.EmitGeometry != nil {
		in.EmitGeometry()
	}
}

// placeHeld appends the ToNext fan-out beads (held value) to items WITHOUT driving
// them, returning the extended set. Invariant: noValue (the empty-Held sentinel) is
// never sent on an output channel — a fire whose Held is noValue places nothing on
// ToNext. Only the SEND is suppressed; Held still updates to the received value in
// the caller. The caller drives these together with the feedback bead in ONE
// Wiring.DriveAll so every outbound bead animates concurrently and the node
// goroutine blocks once (for the fan-out flight) rather than once per edge.
func placeHeld(outs Wiring.OutMulti, held int, items []Wiring.DriveItem) []Wiring.DriveItem {
	if held == noValue {
		return items
	}
	return outs.PlaceDrivenAll(held, items)
}

func (in *Node) Update(ctx context.Context) {
	in.tryEmitGeometry()

	// -1 is the sentinel meaning "no value seen yet"; real values are non-negative
	// indices, so noValue never collides with a legitimate Init index.
	held := noValue
	// Emit the initial interior bead state: held == noValue → present=false (empty
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
		// unconsumed pulse, park briefly and retry — do not consume the input
		// pulse yet. This prevents drops when output transit time exceeds the
		// loop's input rate. A short sleep breaks the busy-spin.
		anyOccupied := false
		for _, out := range in.ToNext {
			if out.Occupied() {
				anyOccupied = true
				break
			}
		}
		if anyOccupied {
			time.Sleep(occupiedPollInterval)
			continue
		}

		if value, ok := in.FromPrevHoldNewSendOldNode.TryRecv(); ok {
			in.Fire()
			in.FromPrevHoldNewSendOldNode.Done()

			// Interior held-value bead: emit only when the held value changes
			// (-1 → 0 → 1 → 0 …). `held` is the running compare value tracking the
			// received value; update it once here at recv time.
			heldChanged := value != held
			held = value
			if heldChanged && in.EmitHeldBead != nil {
				in.EmitHeldBead(value)
			}

			// Drive the ToNext fan-out beads concurrently on THIS goroutine:
			// place them WITHOUT walkers, then drive them all together in one
			// Wiring.DriveAll. prevHeld is the OLD held value (captured before
			// updating in.Held) so the ordering is explicit and obviously correct.
			var items []Wiring.DriveItem
			prevHeld := in.Held
			items = placeHeld(in.ToNext, prevHeld, items)
			in.Held = value
			Wiring.DriveAll(ctx, items)
		}
	}
}

func init() {
	Wiring.Register("HoldNewSendOld", func() any { return &Node{} })
}
