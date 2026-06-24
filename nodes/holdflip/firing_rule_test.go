package holdflip

import (
	"context"
	"sync"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
	"github.com/dtauraso/wirefold/nodes/Wiring"
)

// firstFlip sends value into a node and returns the first non-sentinel output
// (i.e., the first output after input arrives, which should be 1-value).
// The DRIVE goroutine emits -1 sentinel until held is set; we skip those.
func firstFlip(value int) (int, error) {
	tr := T.New(0)
	defer tr.Close()
	in := make(chan int, 1)
	out := make(chan int, 8)
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
	deadline := time.After(200 * time.Millisecond)
	for {
		select {
		case v := <-out:
			if v == -1 {
				continue // sentinel: no input yet, keep waiting
			}
			cancel()
			wg.Wait()
			return v, nil
		case <-deadline:
			cancel()
			wg.Wait()
			return -1, nil
		}
	}
}

// value=1 → output should be 1-1=0.
func TestFlipOneToZero(t *testing.T) {
	got, _ := firstFlip(1)
	if got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}

// value=0 → output should be 1-0=1.
func TestFlipZeroToOne(t *testing.T) {
	got, _ := firstFlip(0)
	if got != 1 {
		t.Fatalf("expected 1, got %d", got)
	}
}

// TestDrainToLatest verifies that when multiple values are queued (simulating a
// Pulse flood), HoldFlip acts on the LATEST value. Queue: 0,0,0,1 → latest is 1
// → output should be 1-1=0, not 1-0=1. Also verifies the interior bead reflects
// the latest input value.
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

	// Expect the first non-sentinel output to be 0 (1-1, because latest input is 1).
	deadline := time.After(500 * time.Millisecond)
	var firstReal int = -99
	for firstReal == -99 {
		select {
		case v := <-out:
			if v == -1 {
				continue // sentinel, skip
			}
			firstReal = v
		case <-deadline:
			cancel()
			wg.Wait()
			t.Fatal("timed out waiting for output")
		}
	}
	cancel()
	wg.Wait()

	if firstReal != 0 {
		t.Fatalf("expected output 0 (latest input was 1, flip → 0), got %d", firstReal)
	}

	// Held display should reflect the latest input value (1), not any stale value.
	mu.Lock()
	defer mu.Unlock()
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

// TestInteriorBeadUpdatesOnInput verifies that the interior bead (EmitHeldBead)
// updates to the input value when input arrives. The startup sentinel (-1) is
// emitted first; after input arrives, the bead should reflect the input value.
// This is the key property of the two-goroutine split: MAIN updates the display
// the instant input arrives, independent of the DRIVE output cycle.
func TestInteriorBeadUpdatesOnInput(t *testing.T) {
	tr := T.New(0)
	defer tr.Close()
	in := make(chan int, 1)
	out := make(chan int, 8)
	beadCh := make(chan int, 8)
	node := &Node{
		Fire: func() { tr.Fire("hf") },
		In:   Wiring.NewIn(in, "hf", "In", tr),
		Out:  Wiring.NewOut(out, "hf", "Out", tr),
		EmitHeldBead: func(v int) {
			beadCh <- v
		},
	}
	// Pre-load input before starting so TryRecv (non-blocking in chan mode) finds it.
	in <- 1

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	// Collect bead updates: expect startup sentinel (-1) then input value (1).
	var beads []int
	deadline := time.After(200 * time.Millisecond)
loop:
	for len(beads) < 2 {
		select {
		case v := <-beadCh:
			beads = append(beads, v)
		case <-deadline:
			break loop
		}
	}
	cancel()
	wg.Wait()

	if len(beads) < 2 {
		t.Fatalf("expected at least 2 bead updates (sentinel + input), got %v", beads)
	}
	if beads[0] != -1 {
		t.Fatalf("expected first bead to be sentinel -1, got %d", beads[0])
	}
	// Find the first non-sentinel bead — should be the input value 1.
	last := -1
	for _, v := range beads {
		if v != -1 {
			last = v
			break
		}
	}
	if last != 1 {
		t.Fatalf("expected interior bead to show input value 1, got %d (sequence: %v)", last, beads)
	}
}
