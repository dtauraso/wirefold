package holdflip

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

// TestFlipRoundTripLean covers holdflip's core contract on the one real
// clock: it holds the last received input and continuously drives the
// FLIPPED value (1-held) out. Feed 0 -> expect 1 on Out; feed 1 -> expect 0.
func TestFlipRoundTripLean(t *testing.T) {
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

	node := &Node{
		Fire: func() {},
		In:   Wiring.NewInPaced(inPw, ctx, "hf", "In", tr),
		Out: Wiring.NewPacedOutNoGeom(outPw, ctx, "hf", "Out", tr,
			Wiring.RuleFireAndForget, latMs*Wiring.PulseSpeedWuPerMs, latMs, ""),
	}
	observer := Wiring.NewInPaced(outPw, ctx, "obs", "In", tr)

	done := make(chan struct{})
	go func() { node.Update(ctx); close(done) }()

	expectFlip := func(want int) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if v, ok := observer.PollRecv(); ok && v == want {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("timeout waiting for flipped value %d", want)
	}

	if !inPw.PlaceDeliverOnly(0, 0) {
		t.Fatal("PlaceDeliverOnly returned false")
	}
	expectFlip(1) // 1-0 = 1

	if !inPw.PlaceDeliverOnly(1, 0) {
		t.Fatal("PlaceDeliverOnly returned false")
	}
	expectFlip(0) // 1-1 = 0

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("node.Update did not exit after cancel")
	}
}
