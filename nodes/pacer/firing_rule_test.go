package pacer

import (
	"context"
	"sync"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
	"github.com/dtauraso/wirefold/nodes/Wiring"
)

// pacedPacerRig drives a Pacer node over real PacedWires + a FakeClock, so
// timing tests can prove the non-blocking Update loop reproduces the same
// per-tick traversal cadence the old blocking DriveAll path did.
type pacedPacerRig struct {
	clk      *Wiring.FakeClock
	inPw     *Wiring.PacedWire
	observer *Wiring.In
	cancel   context.CancelFunc
	wg       *sync.WaitGroup
	ctx      context.Context
	node     *Node
}

// newPacedPacerRig wires a Pacer over real PacedWires + a FakeClock, optionally
// wiring emitHeldBead so held-bead tests can observe EmitHeldBead calls. The
// fireTicks channel records clk.Tick() at the moment Fire() runs (i.e. the
// tick the Pacer receives its input and places the FeedbackOut bead), so
// timing tests can anchor expected arrival ticks to that placement tick
// instead of an independently-driven reference wire (which would double-count
// the separate FromInput wire's own delivery latency).
func newPacedPacerRig(t *testing.T, latMs float64, emitHeldBead func(int)) (*pacedPacerRig, <-chan int64) {
	t.Helper()
	clk := Wiring.NewFakeClock()
	tr := T.New(0)
	ctx, cancel := context.WithCancel(context.Background())

	inPw := Wiring.NewPacedWire(latMs*Wiring.PulseSpeedWuPerMs, Wiring.PulseSpeedWuPerMs)
	inPw.SetClock(clk)
	inPw.Trace = tr
	outPw := Wiring.NewPacedWire(latMs*Wiring.PulseSpeedWuPerMs, Wiring.PulseSpeedWuPerMs)
	outPw.SetClock(clk)
	outPw.Trace = tr

	fireTicks := make(chan int64, 16)
	node := &Node{
		Fire:         func() { fireTicks <- clk.Tick() },
		EmitHeldBead: emitHeldBead,
		FromInput:    Wiring.NewInPaced(inPw, ctx, "pacer", "FromInput", tr),
		FeedbackOut: Wiring.NewPacedOutNoGeom(outPw, ctx, "pacer", "FeedbackOut", tr,
			Wiring.RuleFireAndForget, latMs*Wiring.PulseSpeedWuPerMs, latMs, ""),
	}
	observer := Wiring.NewInPaced(outPw, ctx, "obs", "In", tr)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	return &pacedPacerRig{clk: clk, inPw: inPw, observer: observer, cancel: cancel, wg: &wg, ctx: ctx, node: node}, fireTicks
}

// newPacedPacerRigLat is newPacedPacerRig without a held-bead hook, used by
// the traversal-timing tests below that don't observe EmitHeldBead.
func newPacedPacerRigLat(t *testing.T, latMs float64) (*pacedPacerRig, <-chan int64) {
	t.Helper()
	return newPacedPacerRig(t, latMs, nil)
}

// advanceUntilArrival advances the rig's FakeClock tick-by-tick, settle-polling
// the observer after each tick (mirroring the settle logic in
// TestPacerPacedArrivalTickMatchesTraversal) until a FeedbackOut bead is
// delivered, returning its value.
func advanceUntilArrival(t *testing.T, r *pacedPacerRig) int {
	t.Helper()
	const maxTicks = 20000
	const settleWindow = 5 * time.Millisecond
	const settlePoll = 50 * time.Microsecond
	for tick := 0; tick < maxTicks; tick++ {
		r.clk.AdvanceTicks(1)
		deadline := time.Now().Add(settleWindow)
		for {
			if v, ok := r.observer.PollRecv(); ok {
				return v
			}
			if time.Now().After(deadline) {
				break
			}
			time.Sleep(settlePoll)
		}
	}
	t.Fatal("bead did not arrive within maxTicks")
	return 0
}

func (r *pacedPacerRig) close() {
	r.cancel()
	r.wg.Wait()
}

// TestPacerPacedArrivalTickMatchesTraversal proves the non-blocking
// (place-then-StepOnce-per-tick) FeedbackOut rewrite delivers each bead after
// the SAME fixed traversal-tick count from its placement tick, for two
// separate inputs fed one after the other. Constant, input-independent
// traversal duration is exactly what the old blocking DriveAll path
// guaranteed (a fixed-speed wire); if the non-blocking per-tick pump
// introduced idle-loop drift or accumulated busy-poll overhead, the second
// traversal's duration would differ from the first. The placement tick is
// read directly from Fire() (the exact moment the Pacer receives its input
// and calls PlaceDriven), isolating the FeedbackOut wire's own traversal from
// the separate FromInput wire's delivery latency.
//
// The arrival tick is measured deterministically: after each AdvanceTicks(1)
// the test SETTLES (bounded retry-poll, mirroring
// Wiring.driveUntilAllDelivered) until the pacer's own goroutine has had a
// chance to run its StepOnce for that tick, instead of a single
// fixed-duration sleep. A single short sleep races the pacer goroutine's
// scheduling (worse under -race, which slows every goroutine down non
// uniformly): if the goroutine hasn't yet appended to PacedWire.delivered by
// the time the sleep expires, the observation is deferred to the NEXT
// AdvanceTicks call, which shifts the recorded arrival tick by +1 for
// whichever of the two traversals loses that particular scheduling race —
// producing the intermittent d1 != d2 (this was a TEST measurement race, not
// a product bug in the pacer's per-cycle loop: case A).
func TestPacerPacedArrivalTickMatchesTraversal(t *testing.T) {
	const latMs = 160.0

	r, fireTicks := newPacedPacerRigLat(t, latMs)
	defer r.close()

	if !r.inPw.PlaceAndDriveDeliverOnly(r.ctx, 5, 0) {
		t.Fatal("PlaceAndDriveDeliverOnly first PlaceAndDriveDeliverOnly returned false")
	}

	var placeTicks, arriveTicks []int64
	const maxTicks = 20000
	const settleWindow = 5 * time.Millisecond
	const settlePoll = 50 * time.Microsecond
	var tick int64
	for tick = 0; tick < maxTicks && len(arriveTicks) < 2; tick++ {
		r.clk.AdvanceTicks(1)

		// Drain any Fire() that landed on this tick.
		select {
		case pt := <-fireTicks:
			placeTicks = append(placeTicks, pt)
		default:
		}

		// Settle: give the pacer's own goroutine a bounded window to run its
		// StepOnce for this tick and append to PacedWire.delivered before
		// deciding "no arrival this tick" — a single fixed sleep races that
		// goroutine's scheduling instead of deterministically observing it.
		deadline := time.Now().Add(settleWindow)
		for {
			// A Fire() can also land during the settle window (the input
			// wire's own delivery may be mid-flight relative to the tick
			// loop) — keep draining it so placeTicks stays in step.
			select {
			case pt := <-fireTicks:
				placeTicks = append(placeTicks, pt)
			default:
			}
			if v, ok := r.observer.PollRecv(); ok {
				if len(arriveTicks) == 0 && v != 1 {
					t.Fatalf("expected step=1 (first recv), got %d", v)
				}
				arriveTicks = append(arriveTicks, r.clk.Tick())
				if len(arriveTicks) == 1 {
					// Feed a second, different value once the first is
					// delivered so a second traversal starts.
					if !r.inPw.PlaceAndDriveDeliverOnly(r.ctx, 9, 0) {
						t.Fatal("second PlaceAndDriveDeliverOnly returned false")
					}
				}
				break
			}
			if time.Now().After(deadline) {
				break
			}
			time.Sleep(settlePoll)
		}
	}
	if len(placeTicks) < 2 {
		t.Fatalf("Fire did not run twice (got %d): placeTicks=%v", len(placeTicks), placeTicks)
	}
	if len(arriveTicks) < 2 {
		t.Fatalf("bead did not arrive twice within %d ticks: arriveTicks=%v", maxTicks, arriveTicks)
	}

	d1 := arriveTicks[0] - placeTicks[0]
	d2 := arriveTicks[1] - placeTicks[1]
	if d1 <= 0 {
		t.Fatalf("non-positive traversal duration: %d", d1)
	}
	if d1 != d2 {
		t.Errorf("traversal duration not constant across inputs: first=%d second=%d (placeTicks=%v arriveTicks=%v)",
			d1, d2, placeTicks, arriveTicks)
	}
}

// TestPacerPacedCancelExitsPromptly proves the Update goroutine is NOT parked
// inside a full traversal: cancelling ctx mid-traversal makes it exit
// promptly without requiring the clock to be advanced through delivery.
func TestPacerPacedCancelExitsPromptly(t *testing.T) {
	const latMs = 160.0
	r, _ := newPacedPacerRigLat(t, latMs)

	if !r.inPw.PlaceAndDriveDeliverOnly(r.ctx, 5, 0) {
		t.Fatal("PlaceAndDriveDeliverOnly returned false")
	}
	r.clk.AdvanceTicks(2)
	time.Sleep(5 * time.Millisecond)

	r.cancel()
	done := make(chan struct{})
	go func() { r.wg.Wait(); close(done) }()
	select {
	case <-done:
		// exited promptly, no further clock advancement needed.
	case <-time.After(2 * time.Second):
		t.Fatal("Update goroutine did not exit promptly on ctx cancel (appears parked)")
	}
}

func recv(t *testing.T, ch <-chan int) int {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for FeedbackOut")
		return 0
	}
}

// SPEC contract (pacer/SPEC.md): on each value received on FromInput the node
// fires, computes step = 1 when value != last held (or first recv) else 0, places
// step on FeedbackOut (fire-and-forget), and updates Held. FeedbackOut never
// carries Held — only the step (0 or 1).
func TestPacerChangeStepFeedback(t *testing.T) {
	const latMs = 20.0
	r, _ := newPacedPacerRigLat(t, latMs)
	defer r.close()

	// Sequence: 5 (first recv → step 1), 5 (repeat → step 0), 8 (change → step 1).
	feedAndRecv := func(v int) int {
		if !r.inPw.PlaceAndDriveDeliverOnly(r.ctx, v, 0) {
			t.Fatalf("PlaceAndDriveDeliverOnly(%d) returned false", v)
		}
		return advanceUntilArrival(t, r)
	}

	if got := feedAndRecv(5); got != 1 {
		t.Errorf("first recv (5): expected step 1, got %d", got)
	}
	if got := feedAndRecv(5); got != 0 {
		t.Errorf("repeat (5): expected step 0, got %d", got)
	}
	if got := feedAndRecv(8); got != 1 {
		t.Errorf("change (8): expected step 1, got %d", got)
	}

	if r.node.Held != 8 {
		t.Errorf("Held after last recv: expected 8, got %d", r.node.Held)
	}
}

// The held bead is emitted on startup (sentinel) and re-emitted only when the
// held value changes — a repeated value must not re-emit.
func TestPacerHeldBeadOnChangeOnly(t *testing.T) {
	const latMs = 20.0
	beadCh := make(chan int, 16)
	r, _ := newPacedPacerRig(t, latMs, func(v int) { beadCh <- v })
	defer r.close()

	// Drain FeedbackOut continuously so the paced wire is never back-pressured
	// while this test only cares about held-bead emission timing, and keep the
	// FakeClock advancing so PlaceAndDriveDeliverOnly's self-driven delivery
	// (and the pacer's own WaitTick-paced cycle) can actually make progress.
	go func() {
		for {
			select {
			case <-r.ctx.Done():
				return
			default:
			}
			r.observer.PollRecv()
			r.clk.AdvanceTicks(1)
			time.Sleep(time.Millisecond)
		}
	}()

	if got := recv(t, beadCh); got != noValue {
		t.Fatalf("startup bead: expected sentinel %d, got %d", noValue, got)
	}

	if !r.inPw.PlaceAndDriveDeliverOnly(r.ctx, 3, 0) {
		t.Fatal("PlaceAndDriveDeliverOnly(3) returned false")
	}
	if got := recv(t, beadCh); got != 3 {
		t.Fatalf("held bead after change: expected 3, got %d", got)
	}

	if !r.inPw.PlaceAndDriveDeliverOnly(r.ctx, 3, 0) {
		t.Fatal("PlaceAndDriveDeliverOnly(3) repeat returned false")
	}

	// Give the pacer's paced loop (clock advanced by the drain goroutine
	// above) time to process the repeat value; the held bead must not
	// re-emit for a repeat.
	time.Sleep(50 * time.Millisecond)

	select {
	case v := <-beadCh:
		t.Fatalf("unexpected extra held bead %d (repeat must not re-emit)", v)
	default:
	}
}
