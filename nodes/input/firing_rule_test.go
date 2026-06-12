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
		Fire:       func() { tr.Fire("in") },
		Init:       []int{10, 20, 30},
		ToReadGate: Wiring.NewOut(toRG, "in", "ToReadGate", tr),
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

// sendFeedback places a feedback step on the paced FeedbackIn wire and drives
// Go's clock past the bead's in-flight time so it lands in the slot, where the
// node's blocking TryRecv picks it up. Mirrors the paced-wire send helper used
// by the other node tests.
func sendFeedback(t *testing.T, pw *Wiring.PacedWire, v int) {
	t.Helper()
	clk := Wiring.NewFakeClock()
	pw.SetClock(clk)
	const inFlightMs = 10
	if err := pw.SendDeliverOnly(context.Background(), v, inFlightMs); err != nil {
		t.Fatalf("SendDeliverOnly: %v", err)
	}
	clk.Advance(inFlightMs * time.Millisecond)
	deadline := time.Now().Add(time.Second)
	for pw.InFlight() {
		if time.Now().After(deadline) {
			t.Fatal("clock delivery did not fill feedback slot")
		}
		time.Sleep(time.Millisecond)
	}
}

// Feedback ring: working/backup = [1,0]; end-pop sends 0 then 1, then refills
// and repeats. s==1 pops & sends, s==0 holds (sends nothing). The first emit is
// the unconditional seed that bootstraps the ring (no t=0 deadlock).
func TestFeedbackPopAndHold(t *testing.T) {
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
		Fire:       func() { tr.Fire("in") },
		Init:       []int{1, 0},
		ToReadGate: Wiring.NewOut(toRG, "in", "ToReadGate", tr),
		FeedbackIn: Wiring.NewInPaced(fbPW, ctx, "in", "FeedbackIn", tr),
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()
	defer func() { cancel(); wg.Wait() }()

	// Seed pop fires immediately (unconditional) → emits 0 (end of [1,0]).
	if got := recv(t, toRG); got != 0 {
		t.Fatalf("seed emit: expected 0, got %d", got)
	}

	// Each 1 step pops the next value. Expected after seed (0): 1, 0, 1, 0.
	want := []int{1, 0, 1, 0}
	for i, w := range want {
		sendFeedback(t, fbPW, 1)
		if got := recv(t, toRG); got != w {
			t.Errorf("after 1-step %d: expected emit %d, got %d", i, w, got)
		}
	}

	// A hold step (0) must NOT emit.
	sendFeedback(t, fbPW, 0)
	select {
	case v := <-toRG:
		t.Fatalf("hold step (s==0) emitted %d, expected nothing", v)
	case <-time.After(50 * time.Millisecond):
	}

	// A following 1 step resumes popping. After seed(0) + pops 1,0,1,0 (working
	// just refilled to [1,0]), the next end-pop yields 1.
	sendFeedback(t, fbPW, 1)
	if got := recv(t, toRG); got != 1 {
		t.Errorf("resume after hold: expected 1, got %d", got)
	}
}

// Empty Init: Update returns without emitting anything.
func TestEmptyInit(t *testing.T) {
	tr := T.New(0)
	defer tr.Close()
	toRG := make(chan int, 1)
	node := &Node{
		Fire:       func() { tr.Fire("in") },
		Init:       nil,
		ToReadGate: Wiring.NewOut(toRG, "in", "ToReadGate", tr),
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
