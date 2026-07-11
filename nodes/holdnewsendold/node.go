package holdnewsendold

import (
	"context"
	"runtime"

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

	clk := in.FromPrevHoldNewSendOldNode.Clock()
	if clk == nil {
		// chan mode (unit tests without a paced clock): keep the original
		// blocking ProcessingGuard behavior — its own goroutine drives the
		// output transit while a 1ms timer polls the input port for
		// mid-processing arrivals.
		guard := &Wiring.ProcessingGuard{In: in.FromPrevHoldNewSendOldNode}
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			value, ok := in.FromPrevHoldNewSendOldNode.PollRecv()
			if !ok {
				// Nothing queued yet on either chan or paced mode (PollRecv never
				// blocks) → yield and retry; the top-of-loop ctx check handles exit.
				runtime.Gosched()
				continue
			}
			if in.Fire != nil {
				in.Fire()
			}

			// Interior held-value bead: emit only when the held value changes
			// (-1 → 0 → 1 → 0 …). `held` is the running compare value tracking
			// the received value; update it once here at recv time.
			heldChanged := value != held
			held = value
			if heldChanged && in.EmitHeldBead != nil {
				in.EmitHeldBead(value)
			}

			// Place the ToNext fan-out beads WITHOUT walkers. prevHeld is the OLD
			// held value (captured before updating in.Held) so the ordering is
			// explicit.
			var items []Wiring.DriveItem
			prevHeld := in.Held
			items = placeHeld(in.ToNext, prevHeld, items)
			in.Held = value

			// Run the processing window: drive the placed beads to delivery on an
			// independent goroutine while observing this input port for
			// same/different arrivals. Returns when the output transit completes
			// (window finishes).
			guard.Process(ctx, value, items)
		}
	}

	// Paced mode: single loop, one step per human-clock cycle. windowActive tracks
	// whether the current cycle is inside a processing window — the span from
	// consuming an input value until every placed ToNext bead has finished its
	// transit. While a window is active, the input port is observed
	// non-blockingly each cycle and any arrival (same or different value) is
	// consumed and discarded per the ProcessingGuard rule (input consumption is
	// decoupled from output transit; only the next window's PollRecv consumes a
	// real input). The node is never parked across a traversal — it WaitTicks one
	// human-clock cycle and StepOnces the in-flight ToNext beads exactly once per
	// cycle, matching the canonical single-step shape (nodes/pacer, gatecommon.DriveHeld).
	windowActive := false
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := clk.WaitTick(ctx, clk.Tick()+1); err != nil {
			return
		}

		if windowActive {
			// Mid-window observe: drain and discard every bead delivered on the
			// input port this cycle (same-color and different-color are both
			// consumed silently; neither is processed — matches
			// ProcessingGuard.Process).
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
				items = placeHeld(in.ToNext, prevHeld, items)
				in.Held = value

				// No live bead placed (suppressed sentinel fan-out) ⇒ no real
				// output transit ⇒ no processing window to observe — mirrors
				// ProcessingGuard.Process's early return.
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
		for _, o := range in.ToNext {
			o.StepOnce(ctx)
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
	Wiring.Register("HoldNewSendOld", func() any { return &Node{} })
}
