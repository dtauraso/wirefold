// processing.go — the shared "process one input at a time" mechanism.
//
// MODEL.md: a node does local work and drives its own outputs; it does NOT gate
// input on the output wire clearing (that coupling — the old holdnewsendold
// occupied-poll — was the defect this replaces). A node processes ONE input at a
// time. The PROCESSING WINDOW spans from consuming an input value until that
// input's output bead has finished its transit (the node's own driven delivery).
// The output transit runs INDEPENDENTLY of input consumption: it is driven on its
// own goroutine so it never blocks the node from OBSERVING its input port while a
// prior output is still in flight.
//
// While processing, a new bead arriving on the SAME input port is handled by the
// per-port last-bead rule:
//   - SAME value/color as the port's last bead  → ignored (consumed + discarded).
//   - DIFFERENT value/color                      → the node enters an ERROR state:
//     it emits a node-status event marking its torus RED and carrying the missed
//     bead (value + a world position just outside the node), then discards the bead
//     (it is NOT processed).
// When the node finishes processing (output transit complete), the error state is
// cleared and — if it had been entered — a revert-to-normal node-status event fires.
//
// This is factored as a reusable primitive so other node kinds can adopt the same
// rule without re-deriving it: construct a ProcessingGuard with the observed input
// port and an EmitStatus closure, then call Process once per consumed input.

package Wiring

import (
	"context"
	"time"
)

// processPollInterval bounds the busy-spin of the processing observation loop: how
// often it polls the input port for a new arrival while the output is in transit.
// It is a free scheduling choice (a poll cadence, not a clock), trading CPU burn
// against how promptly a mid-processing arrival is observed.
const processPollInterval = time.Millisecond

// ProcessingGuard encapsulates the shared processing-window mechanism: per-port
// last-bead tracking, the same/different decision for mid-processing arrivals, the
// independent output transit, and the torus-error emit/clear. A node constructs one
// with the input port it observes and an EmitStatus closure (nil-safe), then calls
// Process once per consumed input value.
type ProcessingGuard struct {
	// In is the input port the guard observes for mid-processing arrivals.
	In *In
	// EmitStatus reports the node's torus status: torusRed=true with the missed
	// bead's value on a different-color arrival, torusRed=false to revert. Nil-safe
	// (chan-mode unit tests without an injected closure pass nil).
	EmitStatus func(torusRed bool, missedValue int)
}

// Process runs one processing window. lastVal is the value just consumed from the
// input port (the port's last bead); items are the output beads already PLACED for
// this input (not yet driven). Process drives those beads to delivery on a SEPARATE
// goroutine so the transit proceeds independently, and meanwhile OBSERVES the input
// port for same-port arrivals, applying the same/different rule. It returns when the
// output transit completes (the window finishes) or ctx is canceled. On finish, if
// the error state was entered, it emits the revert-to-normal status.
//
// Beads arriving on the input port during the window are CONSUMED (popped) and
// discarded: a same-color bead silently, a different-color bead after emitting the
// torus-red status. They are never processed — only the next Process call (after this
// window finishes) consumes the next real input.
func (g *ProcessingGuard) Process(ctx context.Context, lastVal int, items []DriveItem) {
	// No live bead to drive ⇒ no real output transit ⇒ no processing window to
	// observe (e.g. a suppressed/empty fan-out, or chan-mode unit tests whose beads
	// already sent synchronously). Return immediately so the node loops straight back
	// to reading its input — observing here would race the next real input and steal
	// it (it would look like a mid-processing arrival when it is the next input).
	live := false
	for _, di := range items {
		if di.live {
			live = true
			break
		}
	}
	if !live {
		return
	}

	driveDone := make(chan struct{})
	go func() {
		DriveAll(ctx, items)
		close(driveDone)
	}()

	errored := false
	finish := func() {
		if errored && g.EmitStatus != nil {
			g.EmitStatus(false, 0)
		}
	}

	// One reused timer for the whole window instead of a fresh time.After per poll
	// iteration (which allocated and abandoned a Timer every ~1ms). Go 1.23 timer
	// semantics make Reset safe without manual channel draining. Stop on return.
	poll := time.NewTimer(processPollInterval)
	defer poll.Stop()

	for {
		// Window complete (output delivered) or torn down? Finish before observing,
		// so a fully-delivered output ends the window promptly. finish() runs on the
		// ctx.Done path too: if the error state was entered, the torus-revert must
		// still emit on cancellation, else a reused stream could stay red.
		select {
		case <-ctx.Done():
			finish()
			return
		case <-driveDone:
			finish()
			return
		default:
		}

		// Non-blocking observe: a bead delivered on the input port mid-processing is
		// consumed + discarded. The output transit (driveDone goroutine) is unaffected.
		if w, ok := g.In.PollRecv(); ok {
			if w != lastVal {
				// Different color → error state: emit torus-red carrying the missed
				// bead, then discard it (it is NOT processed).
				if g.EmitStatus != nil {
					g.EmitStatus(true, w)
				}
				errored = true
			}
			// Same color → ignore. Either way, loop again immediately to drain any
			// further queued arrivals before parking.
			continue
		}

		// Nothing waiting: park briefly, but wake on transit-complete / cancellation.
		poll.Reset(processPollInterval)
		select {
		case <-ctx.Done():
			finish()
			return
		case <-driveDone:
			finish()
			return
		case <-poll.C:
		}
	}
}
