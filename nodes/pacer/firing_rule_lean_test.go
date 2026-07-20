package pacer

import (
	"context"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
	"github.com/dtauraso/wirefold/nodes/Wiring"
)

// stepWire continuously StepOnceAts pw on a short wall-clock poll until ctx is
// cancelled, matching the production per-cycle StepOnceAt delivery path. clk is
// this goroutine's OWN clock copy (docs/planning/visual-editor/per-goroutine-
// clock.md); callers must not share it with another goroutine.
func stepWire(ctx context.Context, pw *Wiring.PacedWire, clk Wiring.Clock) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			pw.StepOnceAt(ctx, clk.Tick())
			time.Sleep(time.Millisecond)
		}
	}()
}

// TestPacerChangeStepFeedbackLean covers pacer's core contract on the one
// real clock: on each received value it fires and emits a change-step
// feedback bead on FeedbackOut — 1 the first time / when the value changes,
// 0 when it repeats.
func TestPacerChangeStepFeedbackLean(t *testing.T) {
	const latMs = 10.0
	tr := T.New(0)
	defer tr.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	inPw := Wiring.NewPacedWire(latMs*Wiring.PulseSpeedWuPerMs, Wiring.PulseSpeedWuPerMs)
	clk := Wiring.NewRealClock()
	stepWire(ctx, inPw, clk.Copy())
	// inSrc is a test-only seeding source on inPw: PlaceDrivenAt places a bead
	// (no walker) that the stepWire loop above then drives to delivery,
	// reusing the production placement API to inject the test's input value.
	inSrc := Wiring.NewPacedOutNoGeom(inPw, ctx, "seed", "Out", tr, Wiring.RuleFireAndForget, 0, 0, "")

	outPw := Wiring.NewPacedWire(latMs*Wiring.PulseSpeedWuPerMs, Wiring.PulseSpeedWuPerMs)

	node := &Node{
		Fire:      func() {},
		Clock:     clk,
		FromInput: Wiring.NewInPaced(inPw, ctx, "pacer", "FromInput", tr),
		FeedbackOut: Wiring.NewPacedOutNoGeom(outPw, ctx, "pacer", "FeedbackOut", tr,
			Wiring.RuleFireAndForget, latMs*Wiring.PulseSpeedWuPerMs, latMs, ""),
	}
	observer := Wiring.NewInPaced(outPw, ctx, "obs", "In", tr)

	done := make(chan struct{})
	go func() { node.Update(ctx); close(done) }()

	waitFor := func(want int) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if v, ok := observer.PollRecv(); ok {
				if v != want {
					t.Fatalf("expected feedback step %d, got %d", want, v)
				}
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("timeout waiting for feedback step %d", want)
	}

	// First value ever seen -> step=1 (change from noValue).
	if !inSrc.PlaceDrivenAt(5, clk.Tick()).Live() {
		t.Fatal("PlaceDrivenAt returned false")
	}
	waitFor(1)

	// Same value again -> step=0.
	if !inSrc.PlaceDrivenAt(5, clk.Tick()).Live() {
		t.Fatal("PlaceDrivenAt returned false")
	}
	waitFor(0)

	// Different value -> step=1.
	if !inSrc.PlaceDrivenAt(6, clk.Tick()).Live() {
		t.Fatal("PlaceDrivenAt returned false")
	}
	waitFor(1)

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("node.Update did not exit after cancel")
	}

	if node.Held != 6 {
		t.Errorf("Held after fires: expected 6, got %d", node.Held)
	}
}
