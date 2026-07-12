package input

import (
	"context"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
	"github.com/dtauraso/wirefold/nodes/Wiring"
)

// TestEmitsInitValuesLean covers input's core plain-emit contract on the one
// real clock (no FeedbackIn wired): it end-pops the working array (a copy of
// Init) each fire, so with Init=[10,20,30] and no Repeat exactly len(init)
// values are emitted end-first: 30, 20, 10; Update then exits.
func TestEmitsInitValuesLean(t *testing.T) {
	const latMs = 10.0
	tr := T.New(0)
	defer tr.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pw := Wiring.NewPacedWire(latMs*Wiring.PulseSpeedWuPerMs, Wiring.PulseSpeedWuPerMs)
	clk := Wiring.NewRealClock()
	pw.SetClock(clk)

	node := &Node{
		Fire:  func() {},
		Init:  []int{10, 20, 30},
		Clock: clk,
		ToHoldNewSendOld: Wiring.NewPacedOutNoGeom(pw, ctx, "in", "ToHoldNewSendOld", tr,
			Wiring.RuleFireAndForget, latMs*Wiring.PulseSpeedWuPerMs, latMs, ""),
	}
	obs := Wiring.NewInPaced(pw, ctx, "obs", "In", tr)

	done := make(chan struct{})
	go func() { node.Update(ctx); close(done) }()

	want := []int{30, 20, 10}
	for i, w := range want {
		deadline := time.Now().Add(3 * time.Second)
		got := false
		for time.Now().Before(deadline) {
			if v, ok := obs.PollRecv(); ok {
				if v != w {
					t.Errorf("value[%d]: expected %d, got %d", i, w, v)
				}
				got = true
				break
			}
			time.Sleep(time.Millisecond)
		}
		if !got {
			t.Fatalf("timeout waiting for value[%d]=%d", i, w)
		}
	}

	// Update exits after all init values are fully delivered (no Repeat).
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("input Node did not finish sending init values in time")
	}
}
