package chaininhibitor

import (
	"context"
	"sync"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
	"github.com/dtauraso/wirefold/nodes/Wiring"
)

// TestEmitHeldBead asserts the interior held-value bead lifecycle: at startup the
// held value is -1 (the bead is absent), and after node 2 receives its first value
// EmitHeldBead fires with that value. Emission happens only on a held-value change.
func TestEmitHeldBead(t *testing.T) {
	tr := T.New(0)
	defer tr.Close()
	fromPrev := make(chan int, 1)
	out0 := make(chan int, 1)

	var mu sync.Mutex
	var emitted []int

	node := &Node{
		Fire:                       func() { tr.Fire("ci") },
		Held:                       -1,
		FromPrevChainInhibitorNode: Wiring.NewIn(fromPrev, "ci", "FromPrevChainInhibitorNode", tr),
		ToNext: Wiring.OutMulti{
			Wiring.NewOut(out0, "ci", "ToNext", tr),
		},
		EmitHeldBead: func(held int) {
			mu.Lock()
			emitted = append(emitted, held)
			mu.Unlock()
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	// Startup emit (held == -1) lands before any input.
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	if len(emitted) != 1 || emitted[0] != -1 {
		mu.Unlock()
		cancel()
		wg.Wait()
		t.Fatalf("startup emit: got %v, want [-1] (present=false)", emitted)
	}
	mu.Unlock()

	// First received value 0 → held changes -1→0, emit fires with 0.
	fromPrev <- 0
	recv(t, out0)
	// Same value 0 again → held unchanged, no new emit.
	fromPrev <- 0
	recv(t, out0)
	// New value 1 → held changes 0→1, emit fires with 1.
	fromPrev <- 1
	recv(t, out0)

	time.Sleep(20 * time.Millisecond)
	cancel()
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	want := []int{-1, 0, 1}
	if len(emitted) != len(want) {
		t.Fatalf("emitted = %v, want %v", emitted, want)
	}
	for i, w := range want {
		if emitted[i] != w {
			t.Fatalf("emitted = %v, want %v", emitted, want)
		}
	}
}
