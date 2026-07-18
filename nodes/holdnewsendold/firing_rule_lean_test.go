package holdnewsendold

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

// TestFireOnReceiveLean covers holdnewsendold's core fire-on-receive
// contract on the one real clock: on receive it fires and forwards the
// PRIOR held value (starts at Held's zero value) to every ToNext fan-out
// entry, then stores the new value in Held.
func TestFireOnReceiveLean(t *testing.T) {
	const latMs = 40.0
	tr := T.New(0)
	defer tr.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	inPw := Wiring.NewPacedWire(latMs*Wiring.PulseSpeedWuPerMs, Wiring.PulseSpeedWuPerMs)
	clk := Wiring.NewRealClock()
	inPw.SetClock(clk)
	stepWire(ctx, inPw)
	// inSrc is a test-only seeding source on inPw: PlaceDriven places a bead
	// (no walker) that the stepWire loop above then drives to delivery,
	// reusing the production placement API to inject the test's input value.
	inSrc := Wiring.NewPacedOutNoGeom(inPw, ctx, "seed", "Out", tr, Wiring.RuleFireAndForget, 0, 0, "")

	outPw0 := Wiring.NewPacedWire(latMs*Wiring.PulseSpeedWuPerMs, Wiring.PulseSpeedWuPerMs)
	outPw0.SetClock(clk)
	outPw1 := Wiring.NewPacedWire(latMs*Wiring.PulseSpeedWuPerMs, Wiring.PulseSpeedWuPerMs)
	outPw1.SetClock(clk)

	node := &Node{
		Fire:                       func() {},
		Held:                       99, // seed a non-zero prior value to forward
		FromPrevHoldNewSendOldNode: Wiring.NewInPaced(inPw, ctx, "in", "FromPrevHoldNewSendOldNode", tr),
		ToNext: Wiring.OutMulti{
			Wiring.NewPacedOutNoGeom(outPw0, ctx, "in", "ToNext0", tr,
				Wiring.RuleFireAndForget, latMs*Wiring.PulseSpeedWuPerMs, latMs, ""),
			Wiring.NewPacedOutNoGeom(outPw1, ctx, "in", "ToNext1", tr,
				Wiring.RuleFireAndForget, latMs*Wiring.PulseSpeedWuPerMs, latMs, ""),
		},
	}
	obs0 := Wiring.NewInPaced(outPw0, ctx, "obs0", "In", tr)
	obs1 := Wiring.NewInPaced(outPw1, ctx, "obs1", "In", tr)

	done := make(chan struct{})
	go func() { node.Update(ctx); close(done) }()

	if !inSrc.PlaceDriven(7).Live() {
		t.Fatal("PlaceDriven returned false")
	}

	waitFor := func(obs *Wiring.In, want int) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if v, ok := obs.PollRecv(); ok {
				if v != want {
					t.Fatalf("expected %d, got %d", want, v)
				}
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("timeout waiting for value %d", want)
	}

	waitFor(obs0, 99)
	waitFor(obs1, 99)

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("node.Update did not exit after cancel")
	}

	if node.Held != 7 {
		t.Errorf("Held after fire: expected 7, got %d", node.Held)
	}
}
