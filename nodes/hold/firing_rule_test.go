package hold

import (
	"context"
	"sync"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
	"github.com/dtauraso/wirefold/nodes/Wiring"
)

// recvBead reads the next held-bead value emitted via EmitHeldBead, bounding the
// wait as a hang guard (the node always eventually emits once input is delivered).
func recvBead(t *testing.T, ch <-chan int) int {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for held bead")
		return 0
	}
}

// pacedRig wires a Hold node's In over a real PacedWire + FakeClock, so tests
// exercise the sleep-timer-paced (WaitTick) Update loop instead of a chan-mode
// busy-poll fallback (mirrors holdnewsendold's newPacedRig).
type pacedRig struct {
	clk    *Wiring.FakeClock
	inPw   *Wiring.PacedWire
	node   *Node
	ctx    context.Context
	cancel context.CancelFunc
	wg     *sync.WaitGroup
}

func newPacedHoldRig(t *testing.T, beadCh chan int, fires *int, mu *sync.Mutex) *pacedRig {
	t.Helper()
	const latMs = 20.0
	clk := Wiring.NewFakeClock()
	tr := T.New(0)
	ctx, cancel := context.WithCancel(context.Background())

	inPw := Wiring.NewPacedWire(latMs*Wiring.PulseSpeedWuPerMs, Wiring.PulseSpeedWuPerMs)
	inPw.SetClock(clk)
	inPw.Trace = tr

	node := &Node{
		Fire: func() {
			if mu != nil {
				mu.Lock()
				*fires++
				mu.Unlock()
			}
		},
		In:           Wiring.NewInPaced(inPw, ctx, "hold", "In", tr),
		EmitHeldBead: func(v int) { beadCh <- v },
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	return &pacedRig{clk: clk, inPw: inPw, node: node, ctx: ctx, cancel: cancel, wg: &wg}
}

func (r *pacedRig) close() {
	r.cancel()
	r.wg.Wait()
}

// drive advances the fake clock in small steps until deadline, so the node's
// WaitTick-paced loop observes deliveries without requiring an exact tick count.
func (r *pacedRig) drive(t *testing.T, steps int) {
	t.Helper()
	for i := 0; i < steps; i++ {
		r.clk.AdvanceTicks(1)
		time.Sleep(200 * time.Microsecond)
	}
}

// SPEC contract (hold/SPEC.md): terminal node, no output. On each value received
// on In it fires, updates Held, and re-emits the held bead WHEN the value changes.
// Startup emits noValue (-1, empty interior).
func TestHoldFiresAndHoldsOnReceive(t *testing.T) {
	beadCh := make(chan int, 16)
	fires := 0
	var mu sync.Mutex

	r := newPacedHoldRig(t, beadCh, &fires, &mu)

	if !r.inPw.PlaceAndDriveDeliverOnly(r.ctx, 7, 0) {
		t.Fatal("PlaceAndDriveDeliverOnly returned false")
	}
	go r.drive(t, 20000)

	// Startup emits the empty-interior sentinel first.
	if got := recvBead(t, beadCh); got != noValue {
		t.Fatalf("startup bead: expected sentinel %d, got %d", noValue, got)
	}
	// After input arrives (7 != held -1) the changed held bead is emitted.
	if got := recvBead(t, beadCh); got != 7 {
		t.Fatalf("held bead after input: expected 7, got %d", got)
	}

	r.close()

	if r.node.Held != 7 {
		t.Errorf("Held after fire: expected 7, got %d", r.node.Held)
	}
	mu.Lock()
	defer mu.Unlock()
	if fires < 1 {
		t.Errorf("expected Fire to be called at least once, got %d", fires)
	}
}

// A repeated value does NOT re-emit the held bead (only changes do). Feeding
// 7 then 7 yields exactly one non-sentinel bead emission.
func TestHoldSuppressesUnchangedReemit(t *testing.T) {
	beadCh := make(chan int, 16)

	r := newPacedHoldRig(t, beadCh, nil, nil)

	if !r.inPw.PlaceAndDriveDeliverOnly(r.ctx, 7, 0) {
		t.Fatal("first PlaceAndDriveDeliverOnly returned false")
	}
	go r.drive(t, 30000)

	// startup sentinel, then a single 7 (the second 7 is unchanged → no re-emit).
	if got := recvBead(t, beadCh); got != noValue {
		t.Fatalf("startup bead: expected %d, got %d", noValue, got)
	}
	if got := recvBead(t, beadCh); got != 7 {
		t.Fatalf("first held bead: expected 7, got %d", got)
	}

	if !r.inPw.PlaceAndDriveDeliverOnly(r.ctx, 7, 0) {
		t.Fatal("second PlaceAndDriveDeliverOnly returned false")
	}

	// Give the paced loop time to (not) emit a duplicate.
	time.Sleep(200 * time.Millisecond)
	r.close()

	select {
	case v := <-beadCh:
		t.Fatalf("unexpected extra bead emission %d (unchanged value must not re-emit)", v)
	default:
	}
}
