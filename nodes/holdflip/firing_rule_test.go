package holdflip

import (
	"context"
	"sync"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
	"github.com/dtauraso/wirefold/nodes/Wiring"
)

func run(value int) (int, error) {
	tr := T.New(0)
	defer tr.Close()
	in := make(chan int, 1)
	out := make(chan int, 1)
	node := &Node{
		Fire: func() { tr.Fire("hf") },
		In:   Wiring.NewIn(in, "hf", "In", tr),
		Out:  Wiring.NewOut(out, "hf", "Out", tr),
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()
	in <- value
	select {
	case v := <-out:
		cancel()
		wg.Wait()
		return v, nil
	case <-time.After(100 * time.Millisecond):
		cancel()
		wg.Wait()
		return -1, nil
	}
}

// value=1 → fires 0.
func TestFlipOneToZero(t *testing.T) {
	got, _ := run(1)
	if got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}

// value=0 → fires 1.
func TestFlipZeroToOne(t *testing.T) {
	got, _ := run(0)
	if got != 1 {
		t.Fatalf("expected 1, got %d", got)
	}
}

// TestDrainToLatest verifies that when multiple values are queued (simulating a
// Pulse flood), HoldFlip drains to the LATEST and fires exactly once based on it.
// Queue: 0,0,0,1 → latest is 1 → output should be 1-1=0, not 1-0=1.
func TestDrainToLatest(t *testing.T) {
	tr := T.New(0)
	defer tr.Close()
	// Buffer large enough to pre-load the backlog before the node reads.
	in := make(chan int, 8)
	out := make(chan int, 8)
	heldVals := []int{}
	var mu sync.Mutex
	node := &Node{
		Fire: func() { tr.Fire("hf") },
		In:   Wiring.NewIn(in, "hf", "In", tr),
		Out:  Wiring.NewOut(out, "hf", "Out", tr),
		EmitHeldBead: func(v int) {
			mu.Lock()
			heldVals = append(heldVals, v)
			mu.Unlock()
		},
	}
	// Pre-load the backlog: stale 0s followed by the current value 1.
	in <- 0
	in <- 0
	in <- 0
	in <- 1

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	// Expect exactly ONE output value of 0 (1-1), not multiple fires.
	select {
	case v := <-out:
		cancel()
		wg.Wait()
		if v != 0 {
			t.Fatalf("expected output 0 (latest input was 1, flip → 0), got %d", v)
		}
	case <-time.After(200 * time.Millisecond):
		cancel()
		wg.Wait()
		t.Fatal("timed out waiting for output")
	}

	// Verify no second fire came through.
	select {
	case extra := <-out:
		t.Fatalf("expected exactly one output, got a second: %d", extra)
	default:
	}

	// Held display should reflect the latest input value (1), not any stale value.
	mu.Lock()
	defer mu.Unlock()
	// Initial -1 sentinel + the held value 1 (only one non-sentinel update expected).
	lastHeld := -1
	for _, v := range heldVals {
		if v != -1 {
			lastHeld = v
		}
	}
	if lastHeld != 1 {
		t.Fatalf("expected held display to show 1 (latest input), got %d (full sequence: %v)", lastHeld, heldVals)
	}
}
