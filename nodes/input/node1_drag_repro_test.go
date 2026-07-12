package input

import (
	"context"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
	"github.com/dtauraso/wirefold/nodes/Wiring"
)

// TestFeedbackRingDrainsLayoutPortWhileWaitingForFeedback reproduces the node-1
// (Input, nodes/input/node.go) drag-freeze bug: once FeedbackIn is wired,
// Update() takes the updateFeedbackRing path. That function's OUTER loop
// drains n.Layout every pass, but the INNER wait loop (spinning on
// clk.SleepCycle waiting for node 2's feedback step) never polls n.Layout at
// all. When node 2 never fires — exactly what happens while the shared clock
// is halted, since node 2's own pacing loop is also gated on the same clock —
// node 1 parks in the inner loop forever and a drag delivered via
// InjectDirect (nodes/Wiring/node_move.go MoveDispatch.RootMove -> fanCenters
// -> LayoutPort.InjectDirect) never gets drained/applied.
//
// This test drives node.Update directly (no domain feedback ever arrives on
// FeedbackIn, modeling "node 2 never fires because the clock is halted")
// with a real Wiring.LayoutPort wired the way the loader wires it (apply
// callback = applyDirect), and asserts a directly-injected layout message
// gets applied within a generous deadline. It is expected to FAIL on current
// code: the inner wait loop never drains Layout, so applyDirect never runs.
func TestFeedbackRingDrainsLayoutPortWhileWaitingForFeedback(t *testing.T) {
	tr := T.New(0)
	defer tr.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const latMs = 10.0
	// ToHoldNewSendOld must be wired (fanOutPlace/fanOutStepOnce touch it
	// unconditionally) and drained so the outer loop's send never blocks.
	pw := Wiring.NewPacedWire(latMs*Wiring.PulseSpeedWuPerMs, Wiring.PulseSpeedWuPerMs)
	clk := Wiring.NewRealClock()
	pw.SetClock(clk)
	outPort := Wiring.NewPacedOutNoGeom(pw, ctx, "1", "ToHoldNewSendOld", tr,
		Wiring.RuleFireAndForget, latMs*Wiring.PulseSpeedWuPerMs, latMs, "1To2")
	obs := Wiring.NewInPaced(pw, ctx, "2", "FromPrevHoldNewSendOldNode", tr)
	go func() {
		// Drain node 2's inbound so node 1's outer-loop send never blocks the
		// goroutine under test.
		for {
			if _, ok := obs.PollRecv(); !ok {
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Millisecond):
				}
			}
		}
	}()

	// FeedbackIn wired (Wired()==true) but NEVER fed — models node 2 never
	// firing because the shared clock is halted, exactly the production
	// scenario in the bug report.
	fbPW := Wiring.NewPacedWire(latMs*Wiring.PulseSpeedWuPerMs, Wiring.PulseSpeedWuPerMs)
	fbPW.SetClock(clk)
	feedbackIn := Wiring.NewInPaced(fbPW, ctx, "1", "FeedbackIn", tr)

	// Layout port wired the same way the loader wires it (builders.go
	// reflectBuild injects apply/applyDirect, which are unexported fields not
	// reachable from this external package). Without a loader, applyDirect is
	// nil — Handle still DRAINS the inbound channel for a Direct message
	// (it just no-ops the position write), exactly like production. So the
	// drain itself (not the position write) is what this test observes: if
	// node 1's own Update() goroutine is alive and draining its Layout port,
	// our own TryRecv below (racing the node's) will find the message
	// already gone.
	layout := Wiring.NewLayoutPort("1")

	node := &Node{
		Fire:             func() {},
		Init:             []int{1, 0},
		Clock:            clk,
		Layout:           layout,
		ToHoldNewSendOld: outPort,
		FeedbackIn:       feedbackIn,
	}

	done := make(chan struct{})
	go func() { node.Update(ctx); close(done) }()
	defer func() {
		cancel()
		<-done
	}()

	// Give node 1 a few tick periods to reach and park in the inner wait
	// loop (peek+send happens fast; the inner loop is where it then sits
	// forever since FeedbackIn never delivers).
	time.Sleep(5 * 16 * time.Millisecond)

	// Deliver a drag the same way MoveDispatch.RootMove/fanCenters do:
	// a Direct LayoutMsg placed on this node's own inbound channel (the
	// production path is LayoutPort.InjectDirect, which the loader calls with
	// a real world-space vec3; that type is unexported so this external
	// package builds the equivalent LayoutMsg{Direct:true,...} and uses the
	// exported Inject method instead — same inbound channel, same Handle
	// consumption path, DirectCenter left at its zero value since only the
	// drain (not the resulting position) is under test here).
	dragMsg := Wiring.LayoutMsg{Direct: true, DirectReach: 5}
	layout.Inject(dragMsg)

	// Settle, then check ONCE (non-racy): node 1 is the only reader of its own
	// Layout inbox. If its Update() loop drains the port every cycle, the
	// message is gone within ~one tick (16ms); a 500ms settle is ~30 cycles of
	// margin, so a single TryRecv reliably finds the channel EMPTY. If node 1
	// never drains it (the bug — parked in the old inner wait loop), the message
	// just sits there and our one TryRecv finds it still present.
	time.Sleep(500 * time.Millisecond)
	if _, stillThere := layout.TryRecv(); stillThere {
		t.Fatal("node 1's feedback-wait path never drained its Layout port: " +
			"a directly-injected drag was never picked up by node 1's own Update() goroutine " +
			"while it waited for node 2's feedback step")
	}
}
