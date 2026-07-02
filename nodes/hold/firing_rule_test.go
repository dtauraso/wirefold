package hold

import (
	"context"
	"sync"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
	"github.com/dtauraso/wirefold/nodes/Wiring"
)

// recvBead reads the next held-bead value emitted via EmitHeldBead, bounding the
// wait as a hang guard (the node always eventually emits once input is delivered).
func recvBead(t *testing.T, ch <-chan int) int {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for held bead")
		return 0
	}
}

// SPEC contract (hold/SPEC.md): terminal node, no output. On each value received
// on In it fires, updates Held, and re-emits the held bead WHEN the value changes.
// Startup emits noValue (-1, empty interior).
func TestHoldFiresAndHoldsOnReceive(t *testing.T) {
	tr := T.New(0)
	defer tr.Close()
	in := make(chan int, 4)
	beadCh := make(chan int, 16)
	fires := 0
	var mu sync.Mutex

	node := &Node{
		Fire:         func() { mu.Lock(); fires++; mu.Unlock() },
		In:           Wiring.NewIn(in, "hold", "In", tr),
		EmitHeldBead: func(v int) { beadCh <- v },
	}
	// Pre-load input before starting: In.TryRecv is non-blocking in chan mode.
	in <- 7

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	// Startup emits the empty-interior sentinel first.
	if got := recvBead(t, beadCh); got != noValue {
		t.Fatalf("startup bead: expected sentinel %d, got %d", noValue, got)
	}
	// After input arrives (7 != held -1) the changed held bead is emitted.
	if got := recvBead(t, beadCh); got != 7 {
		t.Fatalf("held bead after input: expected 7, got %d", got)
	}

	cancel()
	wg.Wait()

	if node.Held != 7 {
		t.Errorf("Held after fire: expected 7, got %d", node.Held)
	}
	mu.Lock()
	defer mu.Unlock()
	if fires < 1 {
		t.Errorf("expected Fire to be called at least once, got %d", fires)
	}
}

// A repeated value does NOT re-emit the held bead (only changes do). Feeding
// 7 then 7 yields exactly one non-sentinel bead emission.
func TestHoldSuppressesUnchangedReemit(t *testing.T) {
	tr := T.New(0)
	defer tr.Close()
	in := make(chan int, 4)
	beadCh := make(chan int, 16)

	node := &Node{
		Fire:         func() {},
		In:           Wiring.NewIn(in, "hold", "In", tr),
		EmitHeldBead: func(v int) { beadCh <- v },
	}
	in <- 7
	in <- 7

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	// startup sentinel, then a single 7 (the second 7 is unchanged → no re-emit).
	if got := recvBead(t, beadCh); got != noValue {
		t.Fatalf("startup bead: expected %d, got %d", noValue, got)
	}
	if got := recvBead(t, beadCh); got != 7 {
		t.Fatalf("first held bead: expected 7, got %d", got)
	}
	// Give the busy loop time to (not) emit a duplicate.
	time.Sleep(50 * time.Millisecond)
	cancel()
	wg.Wait()

	select {
	case v := <-beadCh:
		t.Fatalf("unexpected extra bead emission %d (unchanged value must not re-emit)", v)
	default:
	}
}
