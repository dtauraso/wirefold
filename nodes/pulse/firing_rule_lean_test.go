package pulse

import (
	"context"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
	"github.com/dtauraso/wirefold/nodes/Wiring"
)

// stepWire continuously StepOnces pw on a short wall-clock poll until ctx is
// cancelled, matching the production per-cycle StepOnce delivery path.
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

// TestPulseDrivesHeldValueLean covers pulse's core contract on the one real
// clock: sample-and-hold. It continuously drives its held value to Out
// (starting with the noValue sentinel), and updates the held value (with an
// immediate interior-bead update) whenever a new value arrives on FromInput.
func TestPulseDrivesHeldValueLean(t *testing.T) {
	const latMs = 10.0
	tr := T.New(0)
	defer tr.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	inPw := Wiring.NewPacedWire(latMs*Wiring.PulseSpeedWuPerMs, Wiring.PulseSpeedWuPerMs)
	clk := Wiring.NewRealClock()
	inPw.SetClock(clk)
	stepWire(ctx, inPw)

	outPw := Wiring.NewPacedWire(latMs*Wiring.PulseSpeedWuPerMs, Wiring.PulseSpeedWuPerMs)
	outPw.SetClock(clk)

	beadCh := make(chan int, 16)
	node := &Node{
		Fire:      func() {},
		FromInput: Wiring.NewInPaced(inPw, ctx, "pulse", "FromInput", tr),
		Out: Wiring.NewPacedOutNoGeom(outPw, ctx, "pulse", "Out", tr,
			Wiring.RuleFireAndForget, latMs*Wiring.PulseSpeedWuPerMs, latMs, ""),
		EmitHeldBead: func(v int) { beadCh <- v },
	}
	observer := Wiring.NewInPaced(outPw, ctx, "obs", "In", tr)

	done := make(chan struct{})
	go func() { node.Update(ctx); close(done) }()

	// Startup emits the empty-interior sentinel first.
	select {
	case v := <-beadCh:
		if v != -1 {
			t.Fatalf("startup bead: expected sentinel -1, got %d", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for startup bead")
	}

	if !inPw.PlaceDeliverOnly(5, 0) {
		t.Fatal("PlaceDeliverOnly returned false")
	}

	// The interior bead updates the instant input arrives.
	select {
	case v := <-beadCh:
		if v != 5 {
			t.Fatalf("held bead after input: expected 5, got %d", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for held bead update")
	}

	// The drive goroutine continuously pulses the held value (5) to Out.
	deadline := time.Now().Add(3 * time.Second)
	got := false
	for time.Now().Before(deadline) {
		if v, ok := observer.PollRecv(); ok && v == 5 {
			got = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !got {
		t.Fatal("timeout waiting for driven output value 5")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("node.Update did not exit after cancel")
	}
}
