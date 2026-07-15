package holdnewsendold

import (
	"context"

	"github.com/dtauraso/wirefold/nodes/Wiring"
	"github.com/dtauraso/wirefold/nodes/gatecommon"
)

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
	// consuming an input value until the placed ToNext beads' own traversal tick
	// count has elapsed. Per MODEL.md §Sending, a node's processing window is a
	// TICK COUNT derived from a formula, not a query of wire occupancy: the
	// window length is ticksToCross (arcLength/pulseSpeed, already computed per
	// wire) of the LONGEST ToNext edge, so it does not ask any wire whether a
	// bead is still in flight. While a window is active, the input port is
	// observed non-blockingly each cycle and any arrival (same or different
	// value) is consumed and discarded (input consumption is decoupled from
	// output transit; only the next window's PollRecv consumes a real input).
	// The node is never parked across a traversal — it WaitTicks one
	// human-clock cycle and StepOnces the in-flight ToNext beads exactly once per
	// cycle, matching the canonical single-step shape (nodes/pacer, gatecommon.DriveHeld).
	windowActive := false
	var windowEndTick int64
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
				// output transit ⇒ no processing window to observe. Otherwise
				// the window length is the LONGEST ToNext edge's ticksToCross
				// (arcLength/pulseSpeed, ms-latency / MsPerTick) counted from
				// this placement tick — a formula over the node's own outputs,
				// not a query of wire state.
				placeTick := clk.Tick()
				var maxTicks float64
				anyLive := false
				for i, di := range items {
					if !di.Live() {
						continue
					}
					anyLive = true
					if t := in.ToNext[i].Geom().SimLatencyMs / Wiring.MsPerTick; t > maxTicks {
						maxTicks = t
					}
				}
				if anyLive {
					windowActive = true
					windowEndTick = placeTick + int64(maxTicks+0.999999)
				}
			}
		}

		// Single loop, one step per cycle: advance every in-flight ToNext output
		// bead exactly one position-step (mirrors nodes/pacer and
		// gatecommon.DriveHeld). A window ends once its tick-count budget has
		// elapsed on the shared clock.
		tick := clk.Tick()
		for _, o := range in.ToNext {
			o.StepOnceAt(ctx, tick)
		}
		if windowActive && tick >= windowEndTick {
			windowActive = false
		}
	}
}

func init() {
	Wiring.Register("HoldNewSendOld", func() any { return &Node{} })
}
