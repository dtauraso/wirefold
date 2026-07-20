package windowandinhibitrightgate

import (
	"context"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
	"github.com/dtauraso/wirefold/nodes/Wiring"
	"github.com/dtauraso/wirefold/nodes/gatecommon"
)

// stepWire continuously StepOnceAts pw on a short wall-clock poll until ctx is
// cancelled, matching the production per-cycle StepOnceAt delivery path (no
// blocking delivery loop). Only needed for the two INPUT wires here: the
// gate's own RunGate loop drives ToPassed's StepOnceAt itself each cycle. clk
// is this goroutine's OWN clock copy (docs/planning/visual-editor/
// per-goroutine-clock.md); callers must not share it with another goroutine.
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

// runGate wires one WindowAndInhibitRightGate instance on a real clock, feeds
// left/right, and waits for the AND result on the observer. Both inputs are
// delivered together (inFlightMs=0), so the window never has to clear; the
// fired VALUE returned is a pure function of (left, right), not of how long
// the window/dwell take in wall-clock time — so the 5s poll deadline below
// only needs to comfortably exceed WindowMs+FireDwellMs, not hit an exact
// boundary. That makes the assertion an outcome check, not a timing race.
func runGate(t *testing.T, left, right int) int {
	t.Helper()
	const latMs = 10.0
	tr := T.New(0)
	defer tr.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clk := Wiring.NewRealClock()

	leftPw := Wiring.NewPacedWire(latMs*Wiring.PulseSpeedWuPerMs, Wiring.PulseSpeedWuPerMs)
	stepWire(ctx, leftPw, clk.Copy())

	rightPw := Wiring.NewPacedWire(latMs*Wiring.PulseSpeedWuPerMs, Wiring.PulseSpeedWuPerMs)
	stepWire(ctx, rightPw, clk.Copy())

	// leftSrc/rightSrc are test-only seeding sources: PlaceDrivenAt places a
	// bead (no walker) that the stepWire loops above then drive to delivery,
	// reusing the production placement API to inject the test's input values.
	leftSrc := Wiring.NewPacedOutNoGeom(leftPw, ctx, "seed", "Out", tr, Wiring.RuleFireAndForget, 0, 0, "")
	rightSrc := Wiring.NewPacedOutNoGeom(rightPw, ctx, "seed", "Out", tr, Wiring.RuleFireAndForget, 0, 0, "")

	outPw := Wiring.NewPacedWire(latMs*Wiring.PulseSpeedWuPerMs, Wiring.PulseSpeedWuPerMs)

	node := &Node{GateNode: gatecommon.GateNode{
		Fire:      func() {},
		Clock:     clk,
		FromLeft:  Wiring.NewInPaced(leftPw, ctx, "irg", "FromLeft", tr),
		FromRight: Wiring.NewInPaced(rightPw, ctx, "irg", "FromRight", tr),
		ToPassed: Wiring.NewPacedOutNoGeom(outPw, ctx, "irg", "ToPassed", tr,
			Wiring.RuleFireAndForget, latMs*Wiring.PulseSpeedWuPerMs, latMs, ""),
	}}
	observer := Wiring.NewInPaced(outPw, ctx, "obs", "In", tr)

	done := make(chan struct{})
	go func() { node.Update(ctx); close(done) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("node.Update did not exit after cancel")
		}
	}()

	if !leftSrc.PlaceDrivenAt(left, clk.Tick()).Live() {
		t.Fatal("PlaceDrivenAt(left) returned false")
	}
	if !rightSrc.PlaceDrivenAt(right, clk.Tick()).Live() {
		t.Fatal("PlaceDrivenAt(right) returned false")
	}

	// Both inputs held → window (3000ms) not an issue since both arrive
	// together; fire-dwell (800ms) still applies before the fire. Generous
	// real-time deadline covers dwell + delivery + CI noise.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if v, ok := observer.PollRecv(); ok {
			return v
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timeout waiting for gate output")
	return -1
}

// TestWindowAndInhibitRightGateFiresLean covers WindowAndInhibitRightGate's
// core contract on the one real clock: the gate is left AND (NOT right). It
// fires 1 (not inhibited) when left=1, right=0, and fires 0 (inhibited by
// right=1) when left=1, right=1. This is a deterministic real-state-outcome
// assertion (fires with a specific value, driven purely by the inputs)
// rather than a timing-boundary assertion; verified reliable at -count=5.
//
// gatecommon's exact tick-boundary window-clear/pause-freeze behavior (the
// deleted FakeClock+AdvanceTicks tests TestPauseFreezesWindowAndDwell,
// TestWindowClear) has NO deterministic real-clock equivalent under the
// one-clock/sleep-only model, so per the no-coarsened-tests rule it is left
// untested here rather than covered with a weaker/flaky substitute.
func TestWindowAndInhibitRightGateFiresLean(t *testing.T) {
	if got := runGate(t, 1, 0); got != 1 {
		t.Fatalf("not inhibited (left=1,right=0): got %d, want 1", got)
	}
	if got := runGate(t, 1, 1); got != 0 {
		t.Fatalf("inhibited (left=1,right=1): got %d, want 0", got)
	}
}
