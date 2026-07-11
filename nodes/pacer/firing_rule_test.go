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
}

// newPacedPacerRigLat wires a Pacer over real PacedWires + a FakeClock. The
// fireTicks channel records clk.Tick() at the moment Fire() runs (i.e. the
// tick the Pacer receives its input and places the FeedbackOut bead), so
// timing tests can anchor expected arrival ticks to that placement tick
// instead of an independently-driven reference wire (which would double-count
// the separate FromInput wire's own delivery latency).
func newPacedPacerRigLat(t *testing.T, latMs float64) (*pacedPacerRig, <-chan int64) {
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
		Fire:      func() { fireTicks <- clk.Tick() },
		FromInput: Wiring.NewInPaced(inPw, ctx, "pacer", "FromInput", tr),
		FeedbackOut: Wiring.NewPacedOutNoGeom(outPw, ctx, "pacer", "FeedbackOut", tr,
			Wiring.RuleFireAndForget, latMs*Wiring.PulseSpeedWuPerMs, latMs, ""),
	}
	observer := Wiring.NewInPaced(outPw, ctx, "obs", "In", tr)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	return &pacedPacerRig{clk: clk, inPw: inPw, observer: observer, cancel: cancel, wg: &wg, ctx: ctx}, fireTicks
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
func TestPacerPacedArrivalTickMatchesTraversal(t *testing.T) {
	const latMs = 160.0

	r, fireTicks := newPacedPacerRigLat(t, latMs)
	defer r.close()

	if !r.inPw.PlaceAndDriveDeliverOnly(r.ctx, 5, 0) {
		t.Fatal("PlaceAndDriveDeliverOnly first PlaceAndDriveDeliverOnly returned false")
	}

	var placeTicks, arriveTicks []int64
	const maxTicks = 20000
	var tick int64
	for tick = 0; tick < maxTicks && len(arriveTicks) < 2; tick++ {
		r.clk.AdvanceTicks(1)
		time.Sleep(20 * time.Microsecond)
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
				// Feed a second, different value once the first is delivered
				// so a second traversal starts.
				if !r.inPw.PlaceAndDriveDeliverOnly(r.ctx, 9, 0) {
					t.Fatal("second PlaceAndDriveDeliverOnly returned false")
				}
			}
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
	tr := T.New(0)
	defer tr.Close()
	in := make(chan int, 8)
	fb := make(chan int, 8)

	node := &Node{
		Fire:        func() {},
		FromInput:   Wiring.NewIn(in, "pacer", "FromInput", tr),
		FeedbackOut: Wiring.NewOut(fb, "pacer", "FeedbackOut", tr),
	}
	// Sequence: 5 (first recv → step 1), 5 (repeat → step 0), 8 (change → step 1).
	in <- 5
	in <- 5
	in <- 8

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	if got := recv(t, fb); got != 1 {
		t.Errorf("first recv (5): expected step 1, got %d", got)
	}
	if got := recv(t, fb); got != 0 {
		t.Errorf("repeat (5): expected step 0, got %d", got)
	}
	if got := recv(t, fb); got != 1 {
		t.Errorf("change (8): expected step 1, got %d", got)
	}

	cancel()
	wg.Wait()

	if node.Held != 8 {
		t.Errorf("Held after last recv: expected 8, got %d", node.Held)
	}
}

// The held bead is emitted on startup (sentinel) and re-emitted only when the
// held value changes — a repeated value must not re-emit.
func TestPacerHeldBeadOnChangeOnly(t *testing.T) {
	tr := T.New(0)
	defer tr.Close()
	in := make(chan int, 8)
	fb := make(chan int, 8)
	beadCh := make(chan int, 16)

	node := &Node{
		Fire:         func() {},
		FromInput:    Wiring.NewIn(in, "pacer", "FromInput", tr),
		FeedbackOut:  Wiring.NewOut(fb, "pacer", "FeedbackOut", tr),
		EmitHeldBead: func(v int) { beadCh <- v },
	}
	in <- 3
	in <- 3

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	// Drain feedback so the loop makes progress.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-fb:
			}
		}
	}()

	if got := recv(t, beadCh); got != noValue {
		t.Fatalf("startup bead: expected sentinel %d, got %d", noValue, got)
	}
	if got := recv(t, beadCh); got != 3 {
		t.Fatalf("held bead after change: expected 3, got %d", got)
	}
	time.Sleep(50 * time.Millisecond)
	cancel()
	wg.Wait()

	select {
	case v := <-beadCh:
		t.Fatalf("unexpected extra held bead %d (repeat must not re-emit)", v)
	default:
	}
}
