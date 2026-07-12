package hold

import (
	"context"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
	"github.com/dtauraso/wirefold/nodes/Wiring"
)

// stepWire continuously StepOnces pw on a short wall-clock poll until ctx is
// cancelled, matching the production per-cycle StepOnce delivery path (no
// blocking delivery loop). The real clock advances on its own, so a placed
// bead is carried to delivery once real time crosses its deadline.
func stepWire(ctx context.Context, pw *Wiring.PacedWire) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			pw.StepOnce(ctx)
			time.Sleep(time.Millisecond)
		}
	}()
}

// TestHoldFiresAndHoldsOnReceiveLean covers hold/SPEC.md's core contract on
// the one real clock: terminal node, no output. Startup emits the empty
// (noValue) interior bead; on a received value it fires and re-emits the
// held bead with the new value; Held reflects the latest received value.
func TestHoldFiresAndHoldsOnReceiveLean(t *testing.T) {
	const latMs = 20.0
	tr := T.New(0)
	defer tr.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pw := Wiring.NewPacedWire(latMs*Wiring.PulseSpeedWuPerMs, Wiring.PulseSpeedWuPerMs)
	clk := Wiring.NewRealClock()
	pw.SetClock(clk)
	stepWire(ctx, pw)

	beadCh := make(chan int, 16)
	fires := 0
	node := &Node{
		Fire:         func() { fires++ },
		In:           Wiring.NewInPaced(pw, ctx, "hold", "In", tr),
		EmitHeldBead: func(v int) { beadCh <- v },
	}

	done := make(chan struct{})
	go func() { node.Update(ctx); close(done) }()

	// Startup emits the empty-interior sentinel first.
	select {
	case v := <-beadCh:
		if v != noValue {
			t.Fatalf("startup bead: expected sentinel %d, got %d", noValue, v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for startup bead")
	}

	if !pw.PlaceDeliverOnly(7, 0) {
		t.Fatal("PlaceDeliverOnly returned false")
	}

	// After input arrives (7 != held -1) the changed held bead is emitted.
	select {
	case v := <-beadCh:
		if v != 7 {
			t.Fatalf("held bead after input: expected 7, got %d", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for held bead")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("node.Update did not exit after cancel")
	}

	if node.Held != 7 {
		t.Errorf("Held after fire: expected 7, got %d", node.Held)
	}
	if fires < 1 {
		t.Errorf("expected Fire to be called at least once, got %d", fires)
	}
}
