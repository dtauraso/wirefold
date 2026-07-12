package holdnewsendold

import (
	"context"
	"sync"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
	"github.com/dtauraso/wirefold/nodes/Wiring"
)

// pacedRig wires a HoldNewSendOld node over real PacedWires + a FakeClock, so
// timing tests can prove the non-blocking (single-loop, WaitTick-paced) Update
// rewrite reproduces the same processing-window semantics the old
// ProcessingGuard (drive goroutine + 1ms observe timer) path enforced: fixed
// output traversal duration, and mid-window input arrivals discarded without
// producing a second output bead or updating Held.
type pacedRig struct {
	clk       *Wiring.FakeClock
	inPw      *Wiring.PacedWire
	observer  *Wiring.In
	observers []*Wiring.In
	node      *Node
	cancel    context.CancelFunc
	wg        *sync.WaitGroup
	ctx       context.Context
}

// newPacedRig builds a HoldNewSendOld node wired over real PacedWires + a
// FakeClock. By default it wires a single ToNext output (r.observer); pass an
// explicit output count via nOuts to wire a fan-out of N ToNext outputs
// (r.observers, one per output) — e.g. to prove the fan-out delivers to
// EVERY entry, mirroring TestFireOnReceive's original chan-mode assertion.
//
// An optional configure func(*Node) may be appended after nOuts to mutate the
// node (e.g. set Held or EmitHeldBead) BEFORE the Update goroutine starts —
// callers must call newPacedRigConfig instead when they need this; nOuts-only
// callers keep the plain two-arg form.
func newPacedRig(t *testing.T, latMs float64, nOuts ...int) (*pacedRig, <-chan int64) {
	t.Helper()
	return newPacedRigConfig(t, latMs, nOutsOf(nOuts), nil)
}

// newPacedRigConfig is newPacedRig with an explicit output count and an
// optional configure hook run on the node before its Update goroutine starts.
func newPacedRigConfig(t *testing.T, latMs float64, n int, configure func(*Node)) (*pacedRig, <-chan int64) {
	t.Helper()
	clk := Wiring.NewFakeClock()
	tr := T.New(0)
	ctx, cancel := context.WithCancel(context.Background())

	inPw := Wiring.NewPacedWire(latMs*Wiring.PulseSpeedWuPerMs, Wiring.PulseSpeedWuPerMs)
	inPw.SetClock(clk)
	inPw.Trace = tr

	fireTicks := make(chan int64, 16)
	var toNext Wiring.OutMulti
	var observers []*Wiring.In
	for i := 0; i < n; i++ {
		outPw := Wiring.NewPacedWire(latMs*Wiring.PulseSpeedWuPerMs, Wiring.PulseSpeedWuPerMs)
		outPw.SetClock(clk)
		outPw.Trace = tr
		toNext = append(toNext, Wiring.NewPacedOutNoGeom(outPw, ctx, "hnso", "ToNext", tr,
			Wiring.RuleFireAndForget, latMs*Wiring.PulseSpeedWuPerMs, latMs, ""))
		observers = append(observers, Wiring.NewInPaced(outPw, ctx, "obs", "In", tr))
	}

	node := &Node{
		Fire:                       func() { fireTicks <- clk.Tick() },
		Held:                       99,
		FromPrevHoldNewSendOldNode: Wiring.NewInPaced(inPw, ctx, "hnso", "FromPrevHoldNewSendOldNode", tr),
		ToNext:                     toNext,
	}
	if configure != nil {
		configure(node)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	return &pacedRig{clk: clk, inPw: inPw, observer: observers[0], observers: observers, node: node, cancel: cancel, wg: &wg, ctx: ctx}, fireTicks
}

// nOutsOf extracts the optional output-count arg (defaulting to 1) shared by
// newPacedRig's variadic nOuts parameter.
func nOutsOf(nOuts []int) int {
	if len(nOuts) > 0 {
		return nOuts[0]
	}
	return 1
}

func (r *pacedRig) close() {
	r.cancel()
	r.wg.Wait()
}

// pollRecv advances the FakeClock one tick at a time (with a short real-time
// sleep so the node's WaitTick-paced goroutine gets scheduled) until obs
// yields a value or maxTicks is exhausted.
func pollRecv(r *pacedRig, obs *Wiring.In, maxTicks int) (int, bool) {
	for i := 0; i < maxTicks; i++ {
		r.clk.AdvanceTicks(1)
		time.Sleep(200 * time.Microsecond)
		if v, ok := obs.PollRecv(); ok {
			return v, true
		}
	}
	return 0, false
}

// TestPacedArrivalTickMatchesTraversal proves the single-loop rewrite delivers
// the ToNext bead after the SAME fixed traversal-tick count from its placement
// tick for two inputs fed one after the other (in.Held forwards the PRIOR
// held value, so both traversals carry the sentinel-suppressed startup value
// only on the very first fire — feed a real Held via a pre-set field so both
// fires place a live bead).
func TestPacedArrivalTickMatchesTraversal(t *testing.T) {
	const latMs = 160.0

	r, fireTicks := newPacedRig(t, latMs)
	defer r.close()

	if !r.inPw.PlaceAndDriveDeliverOnly(r.ctx, 5, 0) {
		t.Fatal("PlaceAndDriveDeliverOnly returned false")
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
			if len(arriveTicks) == 0 && v != 99 {
				t.Fatalf("expected first ToNext bead to carry the initial Held (99), got %d", v)
			}
			arriveTicks = append(arriveTicks, r.clk.Tick())
			if len(arriveTicks) == 1 {
				// Second input, once the first traversal is delivered, starts a
				// second window/traversal.
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
	// Allow ±1 tick of polling jitter: the input port is itself a paced wire
	// polled once per human-clock cycle, so the exact cycle a mid-flight input
	// is observed relative to the prior delivery can differ by one tick without
	// any change to the wire's own fixed-speed traversal duration.
	diff := d1 - d2
	if diff < -1 || diff > 1 {
		t.Errorf("traversal duration not constant across inputs (beyond 1-tick poll jitter): first=%d second=%d (placeTicks=%v arriveTicks=%v)",
			d1, d2, placeTicks, arriveTicks)
	}
}

// TestPacedMidWindowArrivalDiscarded proves the per-tick observe rule
// reproduces ProcessingGuard's mid-processing-window behavior: a bead
// arriving on FromPrevHoldNewSendOldNode WHILE a ToNext traversal is still in
// flight is consumed and discarded — it does not update Held and does not
// produce a second ToNext bead. Only ONE ToNext bead is ever delivered for the
// two inputs fed during the single window.
func TestPacedMidWindowArrivalDiscarded(t *testing.T) {
	const latMs = 400.0 // long enough that the mid-window input reliably lands before delivery.

	r, fireTicks := newPacedRig(t, latMs)
	defer r.close()

	if !r.inPw.PlaceAndDriveDeliverOnly(r.ctx, 5, 0) {
		t.Fatal("first PlaceAndDriveDeliverOnly returned false")
	}
	// Wait for the first fire (window opens) before feeding the mid-window bead.
	// The input value was delivered by PlaceAndDriveDeliverOnly's own
	// AdvanceTicks(0) no-op step, so drive the human clock forward until the
	// node's WaitTick-paced loop observes it.
	var firstFireSeen bool
	for i := 0; i < 100 && !firstFireSeen; i++ {
		r.clk.AdvanceTicks(1)
		time.Sleep(200 * time.Microsecond)
		select {
		case <-fireTicks:
			firstFireSeen = true
		default:
		}
	}
	if !firstFireSeen {
		t.Fatal("first Fire never ran")
	}

	// Advance a few ticks (window open, output still in flight) then feed a
	// second, different-valued input — this must be discarded per the
	// same/different mid-window rule (both are discarded).
	r.clk.AdvanceTicks(3)
	time.Sleep(5 * time.Millisecond)
	if !r.inPw.PlaceAndDriveDeliverOnly(r.ctx, 42, 0) {
		t.Fatal("mid-window PlaceAndDriveDeliverOnly returned false")
	}
	r.clk.AdvanceTicks(3)
	time.Sleep(5 * time.Millisecond)

	// The mid-window input must NOT have caused a second Fire yet (it is
	// discarded, not processed).
	select {
	case pt := <-fireTicks:
		t.Fatalf("mid-window input triggered a second Fire at tick %d — it should have been discarded", pt)
	default:
	}

	// Drain to delivery.
	got := 0
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r.clk.AdvanceTicks(1)
		time.Sleep(200 * time.Microsecond)
		if v, ok := r.observer.PollRecv(); ok {
			got++
			if v != 99 {
				t.Fatalf("delivered ToNext value: got %d, want 99 (prior Held)", v)
			}
			break
		}
	}
	if got != 1 {
		t.Fatalf("expected exactly 1 delivered ToNext bead, observed delivery loop exited with got=%d", got)
	}
	// No second bead follows (the discarded input never opened a new window).
	time.Sleep(20 * time.Millisecond)
	if _, ok := r.observer.PollRecv(); ok {
		t.Fatal("a second ToNext bead was delivered — the mid-window input was wrongly processed")
	}
	// Held is only read here after the node goroutine has exited (r.close), so
	// this read is race-free. The discarded mid-window input (42) must never
	// have updated Held — Update only sets in.Held while accepting a NEW input
	// (windowActive == false), and the 42 arrived while windowActive was true.
	r.close()
	if r.node.Held != 5 {
		t.Fatalf("Held changed from the discarded mid-window input: got %d, want unchanged 5 (the only accepted input)", r.node.Held)
	}
}

// TestPacedCancelExitsPromptly proves the Update goroutine is NOT parked
// inside a full traversal (including a mid-window observe wait): cancelling
// ctx while a ToNext bead is still in flight makes it exit promptly without
// requiring the clock to be advanced through delivery.
func TestPacedCancelExitsPromptly(t *testing.T) {
	const latMs = 400.0
	r, _ := newPacedRig(t, latMs)

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
