package pacer

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
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for FeedbackOut")
		return 0
	}
}

// SPEC contract (pacer/SPEC.md): on each value received on FromInput the node
// fires, computes step = 1 when value != last held (or first recv) else 0, places
// step on FeedbackOut (fire-and-forget), and updates Held. FeedbackOut never
// carries Held — only the step (0 or 1).
func TestPacerChangeStepFeedback(t *testing.T) {
	tr := T.New(0)
	defer tr.Close()
	in := make(chan int, 8)
	fb := make(chan int, 8)

	node := &Node{
		Fire:        func() {},
		FromInput:   Wiring.NewIn(in, "pacer", "FromInput", tr),
		FeedbackOut: Wiring.NewOut(fb, "pacer", "FeedbackOut", tr),
	}
	// Sequence: 5 (first recv → step 1), 5 (repeat → step 0), 8 (change → step 1).
	in <- 5
	in <- 5
	in <- 8

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	if got := recv(t, fb); got != 1 {
		t.Errorf("first recv (5): expected step 1, got %d", got)
	}
	if got := recv(t, fb); got != 0 {
		t.Errorf("repeat (5): expected step 0, got %d", got)
	}
	if got := recv(t, fb); got != 1 {
		t.Errorf("change (8): expected step 1, got %d", got)
	}

	cancel()
	wg.Wait()

	if node.Held != 8 {
		t.Errorf("Held after last recv: expected 8, got %d", node.Held)
	}
}

// The held bead is emitted on startup (sentinel) and re-emitted only when the
// held value changes — a repeated value must not re-emit.
func TestPacerHeldBeadOnChangeOnly(t *testing.T) {
	tr := T.New(0)
	defer tr.Close()
	in := make(chan int, 8)
	fb := make(chan int, 8)
	beadCh := make(chan int, 16)

	node := &Node{
		Fire:         func() {},
		FromInput:    Wiring.NewIn(in, "pacer", "FromInput", tr),
		FeedbackOut:  Wiring.NewOut(fb, "pacer", "FeedbackOut", tr),
		EmitHeldBead: func(v int) { beadCh <- v },
	}
	in <- 3
	in <- 3

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	// Drain feedback so the loop makes progress.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-fb:
			}
		}
	}()

	if got := recv(t, beadCh); got != noValue {
		t.Fatalf("startup bead: expected sentinel %d, got %d", noValue, got)
	}
	if got := recv(t, beadCh); got != 3 {
		t.Fatalf("held bead after change: expected 3, got %d", got)
	}
	time.Sleep(50 * time.Millisecond)
	cancel()
	wg.Wait()

	select {
	case v := <-beadCh:
		t.Fatalf("unexpected extra held bead %d (repeat must not re-emit)", v)
	default:
	}
}
