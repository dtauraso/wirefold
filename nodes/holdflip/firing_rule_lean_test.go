package holdflip

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
	stepWire(ctx, inPw, clk.Copy())
	// inSrc is a test-only seeding source on inPw: PlaceDrivenAt places a bead
	// (no walker) that the stepWire loop above then drives to delivery,
	// reusing the production placement API to inject the test's input value.
	inSrc := Wiring.NewPacedOutNoGeom(inPw, ctx, "seed", "Out", tr, Wiring.RuleFireAndForget, 0, 0, "")

	outPw := Wiring.NewPacedWire(latMs*Wiring.PulseSpeedWuPerMs, Wiring.PulseSpeedWuPerMs)

	node := &Node{
		Fire:  func() {},
		Clock: clk,
		In:    Wiring.NewInPaced(inPw, ctx, "hf", "In", tr),
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

	if !inSrc.PlaceDrivenAt(0, clk.Tick()).Live() {
		t.Fatal("PlaceDrivenAt returned false")
	}
	expectFlip(1) // 1-0 = 1

	if !inSrc.PlaceDrivenAt(1, clk.Tick()).Live() {
		t.Fatal("PlaceDrivenAt returned false")
	}
	expectFlip(0) // 1-1 = 0

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("node.Update did not exit after cancel")
	}
}
