package inhibitor

import (
	"context"
	"sync"
	"testing"

	T "github.com/dtauraso/wirefold/Trace"
	"github.com/dtauraso/wirefold/nodes/Wiring"
)

// TestToNextNotBlockedByFeedback locks in the fire-and-forget contract for
// FeedbackOut: the fire loop must reach the ToNext fan-out and forward the Held
// value WITHOUT waiting for the feedback bead to be consumed/acknowledged.
//
// Harness note: the wired (paced-wire) FeedbackOut path cannot be exercised from
// this package's unit tests. In chan mode Out.Wired() is false (no backing
// PacedWire), so the wired branch in Update is not entered; and the paced
// constructor (NewOutPaced) needs the unexported wireSegment type plus a
// FakeClock that live in package Wiring, which cannot import inhibitor
// (import cycle). The wired path is therefore covered by reasoning (the next
// paced TryRecv blocks until node 1 sends the next value, so the node still
// paces on its input — no busy-loop, no deadlock — and TryEmit drops rather
// than blocking/overwriting when the feedback wire is busy) plus the
// loader-driven ring/cascade tests under nodes/Wiring exercised by
// `go test -race ./...`.
//
// What this test does pin: a FeedbackOut wired in chan mode whose channel is
// NEVER drained does not stall the ToNext fan-out. Because TrySend/TryEmit in
// chan mode are non-blocking and the wired branch is skipped, the held value
// must still reach every ToNext output promptly.
func TestToNextNotBlockedByFeedback(t *testing.T) {
	tr := T.New(0)
	defer tr.Close()
	fromPrev := make(chan int, 1)
	out0 := make(chan int, 1)
	out1 := make(chan int, 1)
	// Unbuffered, never-drained feedback channel: a consume-gate (the old
	// WaitConsumed path) would have parked the fire loop here forever.
	feedback := make(chan int)

	node := &Node{
		Fire:                       func() { tr.Fire("ci") },
		Held:                       99,
		FromPrevInhibitorNode: Wiring.NewIn(fromPrev, "ci", "FromPrevInhibitorNode", tr),
		ToNext: Wiring.OutMulti{
			Wiring.NewOut(out0, "ci", "ToNext", tr),
			Wiring.NewOut(out1, "ci", "ToNext", tr),
		},
		FeedbackOut: Wiring.NewOut(feedback, "ci", "FeedbackOut", tr),
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	fromPrev <- 7
	// These would time out if the fire loop were parked on the feedback gate.
	got0 := recv(t, out0)
	got1 := recv(t, out1)
	cancel()
	wg.Wait()

	if got0 != 99 {
		t.Errorf("ToNext[0]: expected 99, got %d", got0)
	}
	if got1 != 99 {
		t.Errorf("ToNext[1]: expected 99, got %d", got1)
	}
	if node.Held != 7 {
		t.Errorf("Held after fire: expected 7, got %d", node.Held)
	}
}
