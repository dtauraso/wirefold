package startholdnewsendold

import (
	"context"

	"github.com/dtauraso/wirefold/nodes/Wiring"
	"github.com/dtauraso/wirefold/nodes/gatecommon"
)

// Node is a StartHoldNewSendOld: bead/wire behavior is byte-for-byte identical to
// HoldNewSendOld (a pure forwarder holding the last value and re-emitting it on the
// next fire). The ONLY difference is layout-time, and it lives entirely in the move
// dispatcher (nodes/Wiring/node_move.go, equalizeNeighborDistances): when a
// StartHoldNewSendOld node is dragged, it applies the move-distance-equalize update to
// its connected Pulse and time (HoldNewSendOld) neighbors only, with the connected time
// neighbor as the source. That is keyed off this kind name, not off any behavior in
// this file — so the Update loop below is deliberately the same as HoldNewSendOld.
type Node struct {
	Wiring.LayoutHolder
	Fire                       func()
	EmitGeometry               func()
	EmitHeldBead               func(held int)
	Held                       int `wire:"data.state"`
	FromPrevHoldNewSendOldNode *Wiring.In
	ToNext                     Wiring.OutMulti
	ToInput                    *Wiring.Out
	// ToHoldNewSendOld is a declared output back to another HoldNewSendOld
	// node. Intentionally inert (no send logic) — see 5To2/6To2 edge task.
	ToHoldNewSendOld *Wiring.Out
	// FromHoldNewSendOld is a declared input from another HoldNewSendOld
	// node. Intentionally inert (no read logic) — see 5To2/6To2 edge task.
	FromHoldNewSendOld *Wiring.In
	// FromPulse is a declared input from a Pulse node. Intentionally inert
	// (no read logic) — see 6To2 edge task.
	FromPulse *Wiring.In
	// FromHold is a declared input from a Hold node. Intentionally inert
	// (no read logic) — see 7To5 edge task.
	FromHold *Wiring.In
}

// placeHeld appends the ToNext fan-out beads (held value) to items WITHOUT driving
// them, returning the extended set. Invariant: gatecommon.NoValue (the empty-Held
// sentinel) is never sent on an output channel — a fire whose Held is NoValue places
// nothing on ToNext. Only the SEND is suppressed; Held still updates to the received
// value in the caller. The caller drives these together with the feedback bead in ONE
// Wiring.DriveAll so every outbound bead animates concurrently and the node
// goroutine blocks once (for the fan-out flight) rather than once per edge.
// tick is the PINNED current tick (snapshotted once by the caller) so every
// element of the ToNext fan-out stamps the same placementTick instead of
// each independently re-reading the live shared clock — see
// Wiring.OutMulti.PlaceDrivenAllAt.
func placeHeld(outs Wiring.OutMulti, held int, tick int64, items []Wiring.DriveItem) []Wiring.DriveItem {
	if held == gatecommon.NoValue {
		return items
	}
	return outs.PlaceDrivenAllAt(held, tick, items)
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

	clk := in.FromPrevHoldNewSendOldNode.Clock()

	// Paced mode: single loop, one step per human-clock cycle. windowActive tracks
	// whether the current cycle is inside a processing window — the span from
	// consuming an input value until every placed ToNext bead has finished its
	// transit. While a window is active, the input port is observed
	// non-blockingly each cycle and any arrival (same or different value) is
	// consumed and discarded (input consumption is decoupled from output
	// transit; only the next window's PollRecv consumes a real input). The node
	// is never parked across a traversal — it WaitTicks one
	// human-clock cycle and StepOnces the in-flight ToNext beads exactly once per
	// cycle, matching the canonical single-step shape (nodes/pacer, gatecommon.DriveHeld).
	windowActive := false
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := clk.SleepCycle(ctx); err != nil {
			return
		}

		if windowActive {
			// Mid-window observe: drain and discard every bead delivered on the
			// input port this cycle (same-color and different-color are both
			// consumed silently; neither is processed).
			for {
				if _, ok := in.FromPrevHoldNewSendOldNode.PollRecv(); !ok {
					break
				}
			}
		} else {
			value, ok := in.FromPrevHoldNewSendOldNode.PollRecv()
			if ok {
				if in.Fire != nil {
					in.Fire()
				}

				// Interior held-value bead: emit only when the held value
				// changes (-1 → 0 → 1 → 0 …). `held` is the running compare
				// value tracking the received value; update it once here at
				// recv time.
				heldChanged := value != held
				held = value
				if heldChanged && in.EmitHeldBead != nil {
					in.EmitHeldBead(value)
				}

				// Place the ToNext fan-out beads WITHOUT walkers. prevHeld is
				// the OLD held value (captured before updating in.Held) so the
				// ordering is explicit.
				var items []Wiring.DriveItem
				prevHeld := in.Held
				items = placeHeld(in.ToNext, prevHeld, clk.Tick(), items)
				in.Held = value

				// No live bead placed (suppressed sentinel fan-out) ⇒ no real
				// output transit ⇒ no processing window to observe.
				for _, di := range items {
					if di.Live() {
						windowActive = true
						break
					}
				}
			}
		}

		// Single loop, one step per cycle: advance every in-flight ToNext output
		// bead exactly one position-step (mirrors nodes/pacer and
		// gatecommon.DriveHeld). A window ends once no ToNext bead remains
		// in-flight after stepping.
		anyInFlight := false
		tick := clk.Tick()
		for _, o := range in.ToNext {
			o.StepOnceAt(ctx, tick)
			if o.InFlight() {
				anyInFlight = true
			}
		}
		if windowActive && !anyInFlight {
			windowActive = false
		}
	}
}

func init() {
	Wiring.Register("StartHoldNewSendOld", func() any { return &Node{} })
}
