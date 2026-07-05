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
//   - DIFFERENT value/color                      → the bead is discarded silently
//     (it is NOT processed). The node continues its current processing window.
//
// This is factored as a reusable primitive so other node kinds can adopt the same
// rule without re-deriving it: construct a ProcessingGuard with the observed input
// port, then call Process once per consumed input.

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
// last-bead tracking, the same/different decision for mid-processing arrivals, and
// the independent output transit. A node constructs one with the input port it
// observes, then calls Process once per consumed input value.
type ProcessingGuard struct {
	// In is the input port the guard observes for mid-processing arrivals.
	In *In
}

// Process runs one processing window. lastVal is the value just consumed from the
// input port (the port's last bead); items are the output beads already PLACED for
// this input (not yet driven). Process drives those beads to delivery on a SEPARATE
// goroutine so the transit proceeds independently, and meanwhile OBSERVES the input
// port for same-port arrivals, applying the same/different rule. It returns when the
// output transit completes (the window finishes) or ctx is canceled.
//
// Beads arriving on the input port during the window are CONSUMED (popped) and
// discarded: a same-color bead silently, a different-color bead also silently.
// They are never processed — only the next Process call (after this window finishes)
// consumes the next real input.
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

	// One reused timer for the whole window instead of a fresh time.After per poll
	// iteration (which allocated and abandoned a Timer every ~1ms). Go 1.23 timer
	// semantics make Reset safe without manual channel draining. Stop on return.
	// This create/reset/stop-on-defer lifecycle appears once in this file (a
	// single Process call owns one timer for its whole window); there is no
	// second call site to share it with, so it is left inline rather than
	// wrapped in a helper — a wrapper here would trade three plain lines for an
	// extra type/function indirection with no reuse to justify it.
	poll := time.NewTimer(processPollInterval)
	defer poll.Stop()

	for {
		// Window complete (output delivered) or torn down? Check before observing so a
		// fully-delivered output ends the window promptly.
		select {
		case <-ctx.Done():
			return
		case <-driveDone:
			return
		default:
		}

		// Non-blocking observe: a bead delivered on the input port mid-processing is
		// consumed + discarded (same or different color). The output transit is unaffected.
		if _, ok := g.In.PollRecv(); ok {
			// Discard — loop again immediately to drain any further queued arrivals.
			continue
		}

		// Nothing waiting: park briefly, but wake on transit-complete / cancellation.
		poll.Reset(processPollInterval)
		select {
		case <-ctx.Done():
			return
		case <-driveDone:
			return
		case <-poll.C:
		}
	}
}
