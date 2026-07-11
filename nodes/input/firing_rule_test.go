package input

import (
	"context"
	"sync"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
	"github.com/dtauraso/wirefold/nodes/Wiring"
)

func recv(t *testing.T, ch <-chan int) int {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout waiting for output")
		return 0
	}
}

// Plain path (no feedback): pops the END of working each iteration, so the
// stored init drains end-first. With Init=[10,20,30] and no Repeat, exactly
// len(init) values are emitted as 30,20,10 then Update exits.
func TestEmitsInitValues(t *testing.T) {
	tr := T.New(0)
	defer tr.Close()
	toRG := make(chan int, 3)
	node := &Node{
		Fire:             func() { tr.Fire("in") },
		Init:             []int{10, 20, 30},
		ToHoldNewSendOld: Wiring.NewOut(toRG, "in", "ToHoldNewSendOld", tr),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	// Update exits after all init values are sent, so wg.Wait suffices.
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("InputNode did not finish sending init values in time")
	}

	// End-pop of [10,20,30]: 30, then 20, then 10.
	want := []int{30, 20, 10}
	for i, w := range want {
		got := recv(t, toRG)
		if got != w {
			t.Errorf("value[%d]: expected %d, got %d", i, w, got)
		}
	}
}

// feedbackSender installs ONE FakeClock on the paced FeedbackIn wire and returns
// a send function. Each call places a delivery-only bead (no position stream) via
// PlaceAndDriveDeliverOnly, giving it a unique gen. The clock is advanced only
// past the bead's in-flight time so it lands in the slot for the node's blocking TryRecv.
func feedbackSender(t *testing.T, pw *Wiring.PacedWire) func(v int) {
	t.Helper()
	clk := Wiring.NewFakeClock()
	pw.SetClock(clk)
	const inFlightMs = 10
	return func(v int) {
		if !pw.PlaceAndDriveDeliverOnly(context.Background(), v, inFlightMs) {
			t.Fatalf("PlaceAndDriveDeliverOnly returned false")
		}
		clk.AdvanceTicks(inFlightMs)
		deadline := time.Now().Add(time.Second)
		for pw.InFlight() {
			if time.Now().After(deadline) {
				t.Fatal("clock delivery did not fill feedback slot")
			}
			time.Sleep(time.Millisecond)
		}
	}
}

// Feedback ring: action/backup = [1,0]. SENDING peeks the end (no pop), so the
// first send is the normal loop body (peek+send) — it self-starts the ring with
// no seed. The buffer depletes ONLY on s==1: a pop advances the peeked end, so
// the send sequence across s==1 signals is 0, 1, 0, 1, ... (refill resets to 0).
// s==0 holds: same last bead sent again next loop, no pop.
func TestFeedbackPeekSendPopAndHold(t *testing.T) {
	tr := T.New(0)
	defer tr.Close()
	toRG := make(chan int, 16)

	fbPW := Wiring.NewPacedWire(8, Wiring.PulseSpeedWuPerMs)
	fbPW.Target = "in"
	fbPW.TargetHandle = "FeedbackIn"
	fbPW.Trace = tr

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	node := &Node{
		Fire:             func() { tr.Fire("in") },
		Init:             []int{1, 0},
		ToHoldNewSendOld: Wiring.NewOut(toRG, "in", "ToHoldNewSendOld", tr),
		FeedbackIn:       Wiring.NewInPaced(fbPW, ctx, "in", "FeedbackIn", tr),
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()
	defer func() { cancel(); wg.Wait() }()

	// Each feedback step is a DISTINCT signal; the sender steps the one clock past
	// the Recv refractory window between steps so the gate accepts each as its own
	// fire (instead of collapsing the sequence into one train).
	send := feedbackSender(t, fbPW)

	// First loop body: peek the end of [1,0] = 0 and send (no pop, no seed).
	if got := recv(t, toRG); got != 0 {
		t.Fatalf("first send: expected peek 0, got %d", got)
	}

	// Each s==1 pops, advancing the peeked end. Send sequence after the first
	// (0): pop→[1] so next send peeks 1; pop→[] refill→[1,0] so next send peeks
	// 0; then 1; then 0.
	want := []int{1, 0, 1, 0}
	for i, w := range want {
		send(1)
		if got := recv(t, toRG); got != w {
			t.Errorf("after 1-step %d: expected send %d, got %d", i, w, got)
		}
	}

	// A hold step (s==0) does NOT pop — the SAME last bead is sent next loop.
	// Buffer just refilled-then-popped to [1] (peek 1) above; current peek is
	// the value after those four pops. Sending peeks, so a send still happens,
	// but the value must equal the prior peek (no advance).
	send(0)
	if got := recv(t, toRG); got != want[len(want)-1] {
		t.Errorf("hold step: expected same bead %d resent, got %d", want[len(want)-1], got)
	}
}

// With NO feedback delivered after the first send, the node blocks on the
// feedback read: it has sent exactly once (peek, no pop) and the interior stays
// FULL (4 beads) — sending did not consume the buffer.
func TestFeedbackSendDoesNotDeplete(t *testing.T) {
	tr := T.New(0)
	defer tr.Close()
	toRG := make(chan int, 16)

	var mu sync.Mutex
	var snaps []beadSnapshot

	fbPW := Wiring.NewPacedWire(8, Wiring.PulseSpeedWuPerMs)
	fbPW.Target = "in"
	fbPW.TargetHandle = "FeedbackIn"
	fbPW.Trace = tr

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	node := &Node{
		Fire:             func() { tr.Fire("in") },
		Init:             []int{1, 0},
		ToHoldNewSendOld: Wiring.NewOut(toRG, "in", "ToHoldNewSendOld", tr),
		FeedbackIn:       Wiring.NewInPaced(fbPW, ctx, "in", "FeedbackIn", tr),
		EmitNodeBeads: func(working, backup []int) {
			mu.Lock()
			snaps = append(snaps, beadSnapshot{
				working: append([]int(nil), working...),
				backup:  append([]int(nil), backup...),
			})
			mu.Unlock()
		},
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()
	defer func() { cancel(); wg.Wait() }()

	// First send happens (peek 0). No feedback is delivered, so the node blocks
	// on the feedback read — no pop occurs.
	if got := recv(t, toRG); got != 0 {
		t.Fatalf("first send: expected peek 0, got %d", got)
	}

	// Give the blocked read time to (not) do anything.
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	// Only the initial full(4) emit happened — no pop emit.
	if len(snaps) != 1 {
		t.Fatalf("got %d interior snapshots, want 1 (no pop without feedback): %+v", len(snaps), snaps)
	}
	got := snaps[0]
	if len(got.working)+len(got.backup) != 4 {
		t.Errorf("interior = %+v, want FULL 4 beads (send peeked, did not pop)", got)
	}
}

// Empty Init: Update returns without emitting anything.
func TestEmptyInit(t *testing.T) {
	tr := T.New(0)
	defer tr.Close()
	toRG := make(chan int, 1)
	node := &Node{
		Fire:             func() { tr.Fire("in") },
		Init:             nil,
		ToHoldNewSendOld: Wiring.NewOut(toRG, "in", "ToHoldNewSendOld", tr),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout: InputNode with empty Init should exit immediately")
	}

	select {
	case v := <-toRG:
		t.Fatalf("unexpected emission %d", v)
	default:
	}
}

// TestPacedFanOutDeliversConcurrentlyAtSameTick proves the non-blocking
// (place-all-then-StepOnce-per-cycle) fanOut rewrite preserves the old
// blocking DriveAll's concurrent fan-out: two wired outputs
// (ToHoldNewSendOld, ToExcitatory) fed the same value on the same cycle
// arrive at the SAME clock tick, and Update is not parked across the
// traversal — cancelling ctx mid-traversal (before the clock is advanced
// through delivery) exits promptly instead of hanging.
//
// Arrival tick is read via PollRecvTick, which reports the tick captured
// under the wire's own lock at the instant tryDeliverHeadLocked moved the
// bead from inflight to delivered — NOT clk.Tick() re-read by this test's
// polling goroutine after the fact. The two are not equivalent under
// -race/GC-pause scheduling: fanOutStepOnce pins one tick and calls
// StepOnceAt on both wires with it back-to-back, so both deliveries are
// genuinely computed against the identical tick, but this test's own poll
// loop advances the SHARED FakeClock every iteration; if the second wire's
// delivery hasn't propagated to PollRecv yet when polled at iteration N, the
// test moves on and advances the clock to N+1 before catching it, and a
// clk.Tick()-based read would then wrongly record N+1 for a bead that
// actually delivered at N. PollRecvTick removes that measurement race by
// reading the tick from the delivery event itself.
func TestPacedFanOutDeliversConcurrentlyAtSameTick(t *testing.T) {
	const latMs = 160.0

	clk := Wiring.NewFakeClock()
	tr := T.New(0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pw1 := Wiring.NewPacedWire(latMs*Wiring.PulseSpeedWuPerMs, Wiring.PulseSpeedWuPerMs)
	pw1.SetClock(clk)
	pw1.Trace = tr
	pw2 := Wiring.NewPacedWire(latMs*Wiring.PulseSpeedWuPerMs, Wiring.PulseSpeedWuPerMs)
	pw2.SetClock(clk)
	pw2.Trace = tr

	node := &Node{
		Fire: func() { tr.Fire("in") },
		Init: []int{7},
		ToHoldNewSendOld: Wiring.NewPacedOutNoGeom(pw1, ctx, "in", "ToHoldNewSendOld", tr,
			Wiring.RuleFireAndForget, latMs*Wiring.PulseSpeedWuPerMs, latMs, ""),
		ToExcitatory: Wiring.NewPacedOutNoGeom(pw2, ctx, "in", "ToExcitatory", tr,
			Wiring.RuleFireAndForget, latMs*Wiring.PulseSpeedWuPerMs, latMs, ""),
	}
	obs1 := Wiring.NewInPaced(pw1, ctx, "obs1", "In", tr)
	obs2 := Wiring.NewInPaced(pw2, ctx, "obs2", "In", tr)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	var arrive1, arrive2 int64
	got1, got2 := false, false
	const maxTicks = 20000
	var tick int64
	for tick = 0; tick < maxTicks && !(got1 && got2); tick++ {
		clk.AdvanceTicks(1)
		time.Sleep(20 * time.Microsecond)
		if !got1 {
			if v, tick, ok := obs1.PollRecvTick(); ok {
				if v != 7 {
					t.Fatalf("obs1: expected 7, got %d", v)
				}
				arrive1 = tick
				got1 = true
			}
		}
		if !got2 {
			if v, tick, ok := obs2.PollRecvTick(); ok {
				if v != 7 {
					t.Fatalf("obs2: expected 7, got %d", v)
				}
				arrive2 = tick
				got2 = true
			}
		}
	}
	if !got1 || !got2 {
		t.Fatalf("fan-out did not deliver to both outputs within %d ticks (got1=%v got2=%v)", maxTicks, got1, got2)
	}
	if arrive1 != arrive2 {
		t.Errorf("fan-out not concurrent: ToHoldNewSendOld arrived at tick %d, ToExcitatory at tick %d", arrive1, arrive2)
	}

	// Update must exit once the single (non-Repeat) init value has been fully
	// delivered on every fan-out output.
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Update did not exit after the sole init value was fully delivered on both outputs")
	}
}

// TestPacedFanOutCancelExitsPromptly proves the non-blocking loop is never
// parked inside a full traversal: cancelling ctx mid-traversal (before the
// clock has advanced through delivery) makes Update exit promptly.
func TestPacedFanOutCancelExitsPromptly(t *testing.T) {
	const latMs = 160.0

	clk := Wiring.NewFakeClock()
	tr := T.New(0)
	ctx, cancel := context.WithCancel(context.Background())

	pw1 := Wiring.NewPacedWire(latMs*Wiring.PulseSpeedWuPerMs, Wiring.PulseSpeedWuPerMs)
	pw1.SetClock(clk)
	pw1.Trace = tr
	pw2 := Wiring.NewPacedWire(latMs*Wiring.PulseSpeedWuPerMs, Wiring.PulseSpeedWuPerMs)
	pw2.SetClock(clk)
	pw2.Trace = tr

	node := &Node{
		Fire: func() { tr.Fire("in") },
		Init: []int{7},
		ToHoldNewSendOld: Wiring.NewPacedOutNoGeom(pw1, ctx, "in", "ToHoldNewSendOld", tr,
			Wiring.RuleFireAndForget, latMs*Wiring.PulseSpeedWuPerMs, latMs, ""),
		ToExcitatory: Wiring.NewPacedOutNoGeom(pw2, ctx, "in", "ToExcitatory", tr,
			Wiring.RuleFireAndForget, latMs*Wiring.PulseSpeedWuPerMs, latMs, ""),
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	// Advance a couple ticks so the fan-out is placed and mid-traversal, well
	// short of the 160ms delivery deadline.
	clk.AdvanceTicks(2)
	time.Sleep(5 * time.Millisecond)
	cancel()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Update did not exit promptly on ctx cancel (looks parked inside a full traversal)")
	}
}
