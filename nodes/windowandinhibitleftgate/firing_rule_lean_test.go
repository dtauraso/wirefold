package windowandinhibitleftgate

import (
	"context"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
	"github.com/dtauraso/wirefold/nodes/Wiring"
	"github.com/dtauraso/wirefold/nodes/gatecommon"
)

// stepWire continuously StepOnces pw on a short wall-clock poll until ctx is
// cancelled, matching the production per-cycle StepOnce delivery path (no
// blocking delivery loop). Only needed for the two INPUT wires here: the
// gate's own RunGate loop drives ToPassed's StepOnce itself each cycle.
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

// runGate wires one WindowAndInhibitLeftGate instance on a real clock, feeds
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
	leftPw.SetClock(clk)
	stepWire(ctx, leftPw)

	rightPw := Wiring.NewPacedWire(latMs*Wiring.PulseSpeedWuPerMs, Wiring.PulseSpeedWuPerMs)
	rightPw.SetClock(clk)
	stepWire(ctx, rightPw)

	outPw := Wiring.NewPacedWire(latMs*Wiring.PulseSpeedWuPerMs, Wiring.PulseSpeedWuPerMs)
	outPw.SetClock(clk)

	node := &Node{GateNode: gatecommon.GateNode{
		Fire:      func() {},
		FromLeft:  Wiring.NewInPaced(leftPw, ctx, "ilg", "FromLeft", tr),
		FromRight: Wiring.NewInPaced(rightPw, ctx, "ilg", "FromRight", tr),
		ToPassed: Wiring.NewPacedOutNoGeom(outPw, ctx, "ilg", "ToPassed", tr,
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

	if !leftPw.PlaceDeliverOnly(left, 0) {
		t.Fatal("PlaceDeliverOnly(left) returned false")
	}
	if !rightPw.PlaceDeliverOnly(right, 0) {
		t.Fatal("PlaceDeliverOnly(right) returned false")
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

// TestWindowAndInhibitLeftGateFiresLean covers WindowAndInhibitLeftGate's core
// contract on the one real clock: the gate is (NOT left) AND right. It fires
// 1 (not inhibited) when left=0, right=1, and fires 0 (inhibited by left=1)
// when left=1, right=1. This is a deterministic real-state-outcome assertion
// (fires with a specific value, driven purely by the inputs) rather than a
// timing-boundary assertion; verified reliable at -count=5.
//
// gatecommon's exact tick-boundary window-clear/pause-freeze behavior (the
// deleted FakeClock+AdvanceTicks tests TestPauseFreezesWindowAndDwell,
// TestWindowClear) has NO deterministic real-clock equivalent under the
// one-clock/sleep-only model, so per the no-coarsened-tests rule it is left
// untested here rather than covered with a weaker/flaky substitute.
func TestWindowAndInhibitLeftGateFiresLean(t *testing.T) {
	if got := runGate(t, 0, 1); got != 1 {
		t.Fatalf("not inhibited (left=0,right=1): got %d, want 1", got)
	}
	if got := runGate(t, 1, 1); got != 0 {
		t.Fatalf("inhibited (left=1,right=1): got %d, want 0", got)
	}
}
