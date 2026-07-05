package holdnewsendold

import (
	"context"

	"github.com/dtauraso/wirefold/nodes/Wiring"
	"github.com/dtauraso/wirefold/nodes/gatecommon"
)

type Node struct {
	Fire                       func()
	EmitGeometry               func()
	EmitHeldBead               func(held int)
	Held                       int `wire:"data.state"`
	FromPrevHoldNewSendOldNode *Wiring.In
	ToNext                     Wiring.OutMulti
}

// placeHeld appends the ToNext fan-out beads (held value) to items WITHOUT driving
// them, returning the extended set. Invariant: gatecommon.NoValue (the empty-Held
// sentinel) is never sent on an output channel — a fire whose Held is NoValue places
// nothing on ToNext. Only the SEND is suppressed; Held still updates to the received
// value in the caller. The caller drives these together with the feedback bead in ONE
// Wiring.DriveAll so every outbound bead animates concurrently and the node
// goroutine blocks once (for the fan-out flight) rather than once per edge.
func placeHeld(outs Wiring.OutMulti, held int, items []Wiring.DriveItem) []Wiring.DriveItem {
	if held == gatecommon.NoValue {
		return items
	}
	return outs.PlaceDrivenAll(held, items)
}

func (in *Node) Update(ctx context.Context) {
	Wiring.TryEmit(in.EmitGeometry)

	// -1 is the sentinel meaning "no value seen yet"; real values are non-negative
	// indices, so gatecommon.NoValue never collides with a legitimate Init index.
	held := gatecommon.NoValue
	// Emit the initial interior bead state: held == NoValue → present=false (empty
	// interior). The bead is re-emitted only when held actually changes below.
	if in.EmitHeldBead != nil {
		in.EmitHeldBead(held)
	}

	// The shared processing mechanism: it owns the per-input processing window —
	// driving the output transit INDEPENDENTLY (its own goroutine) while observing
	// this input port for same/different mid-processing arrivals, and emitting the
	// torus-red/normal status. No output-occupied backpressure: input consumption is
	// decoupled from output transit (the removed defect).
	guard := &Wiring.ProcessingGuard{
		In: in.FromPrevHoldNewSendOldNode,
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		value, ok := in.FromPrevHoldNewSendOldNode.TryRecv()
		if !ok {
			// chan mode: nothing queued yet → retry. paced mode: TryRecv blocks, so
			// !ok means ctx was canceled → fall through to the top-of-loop ctx check.
			continue
		}
		if in.Fire != nil {
			in.Fire()
		}

		// Interior held-value bead: emit only when the held value changes
		// (-1 → 0 → 1 → 0 …). `held` is the running compare value tracking the
		// received value; update it once here at recv time.
		heldChanged := value != held
		held = value
		if heldChanged && in.EmitHeldBead != nil {
			in.EmitHeldBead(value)
		}

		// Place the ToNext fan-out beads WITHOUT walkers. prevHeld is the OLD held
		// value (captured before updating in.Held) so the ordering is explicit.
		var items []Wiring.DriveItem
		prevHeld := in.Held
		items = placeHeld(in.ToNext, prevHeld, items)
		in.Held = value

		// Run the processing window: drive the placed beads to delivery on an
		// independent goroutine while observing this input port for same/different
		// arrivals. Returns when the output transit completes (window finishes).
		guard.Process(ctx, value, items)
	}
}

func init() {
	Wiring.Register("HoldNewSendOld", func() any { return &Node{} })
}
