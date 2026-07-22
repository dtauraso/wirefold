package holdnewsendold

import (
	"context"

	"github.com/dtauraso/wirefold/nodes/Wiring"
	"github.com/dtauraso/wirefold/nodes/gatecommon"
)

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
	SpeedCh                    <-chan float64
	FromPrevHoldNewSendOldNode *Wiring.In
	ToNext                     Wiring.Broadcast
}

// placeHeld appends the ToNext broadcast beads (held value) to items WITHOUT driving
// them, returning the extended set. Invariant: gatecommon.NoValue (the empty-Held
// sentinel) is never sent on an output channel — a fire whose Held is NoValue places
// nothing on ToNext. Only the SEND is suppressed; Held still updates to the received
// value in the caller. Delivery is timed by each wire's own goroutine, so the whole
// broadcast animates concurrently with no further driving from this node.
func placeHeld(outs Wiring.Broadcast, held int, items []Wiring.DriveItem) []Wiring.DriveItem {
	if held == gatecommon.NoValue {
		return items
	}
	return outs.PlaceDrivenAllAt(held, items)
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

	// Copy taken ONCE at this goroutine's start (Update IS the goroutine): from
	// here on this loop reads only its own clock, never in.Clock (this node's
	// origin field) directly again.
	clk := in.Clock.Copy()

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

		Wiring.ApplySpeedNonBlocking(clk, in.SpeedCh)
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

				// Place the ToNext broadcast beads WITHOUT walkers. prevHeld is
				// the OLD held value (captured before updating in.Held) so the
				// ordering is explicit.
				var items []Wiring.DriveItem
				prevHeld := in.Held
				items = placeHeld(in.ToNext, prevHeld, items)
				in.Held = value

				// No live bead placed (suppressed sentinel broadcast) ⇒ no real
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

		// Each ToNext wire's own goroutine advances its in-flight beads; this
		// node only tracks whether the window's tick-count budget has elapsed.
		if windowActive && clk.Tick() >= windowEndTick {
			windowActive = false
		}
	}
}

func init() {
	// Held defaults to the empty sentinel, not the int zero-value: 0 is a
	// legitimate held value (a real bead), so an unset seed must be empty
	// (NoValue) rather than a phantom 0. The data.state seed overrides this
	// only when the spec authors a real starting value.
	Wiring.Register("HoldNewSendOld", func() any { return &Node{Held: gatecommon.NoValue} })
}
