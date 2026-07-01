package holdnewsendold

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
		FromPrevHoldNewSendOldNode: Wiring.NewIn(fromPrev, "ci", "FromPrevHoldNewSendOldNode", tr),
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

	// waitEmits blocks until at least n interior emits have been observed, or fails
	// after a bounded timeout. Polls the real observable (emitted) rather than
	// sleeping a fixed interval, so the sequencing is guaranteed not assumed.
	waitEmits := func(n int) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for {
			mu.Lock()
			got := len(emitted)
			mu.Unlock()
			if got >= n {
				return
			}
			if time.Now().After(deadline) {
				cancel()
				wg.Wait()
				t.Fatalf("only %d interior emits after wait, want %d", got, n)
			}
			time.Sleep(time.Millisecond)
		}
	}

	// Startup emit (held == -1) lands before any input.
	waitEmits(1)
	mu.Lock()
	if emitted[0] != -1 {
		mu.Unlock()
		cancel()
		wg.Wait()
		t.Fatalf("startup emit: got %v, want [-1] (present=false)", emitted)
	}
	mu.Unlock()

	// First received value 0 → held changes -1→0, interior emit fires with 0.
	// ToNext forwards the PRIOR Held (-1), which is suppressed → no output bead.
	fromPrev <- 0
	select {
	case v := <-out0:
		t.Fatalf("first fire emitted %d on ToNext; expected nothing (Held was -1)", v)
	case <-time.After(60 * time.Millisecond):
	}
	// Same value 0 again → interior held unchanged; ToNext forwards Held 0.
	fromPrev <- 0
	recv(t, out0)
	// New value 1 → interior emit fires with 1; ToNext forwards Held 0.
	fromPrev <- 1
	recv(t, out0)

	// Wait for all three interior emits (-1, 0, 1) to land before tearing down.
	waitEmits(3)
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

// TestNoSentinelOnToNext asserts the output invariant: starting Held=-1, the
// first fire emits NOTHING on ToNext (the -1 sentinel is suppressed), and once
// Held has become a real value (0/1) a subsequent fire DOES emit on ToNext.
func TestNoSentinelOnToNext(t *testing.T) {
	tr := T.New(0)
	defer tr.Close()
	fromPrev := make(chan int, 1)
	out0 := make(chan int, 1)

	node := &Node{
		Fire:                       func() { tr.Fire("ci") },
		Held:                       -1,
		FromPrevHoldNewSendOldNode: Wiring.NewIn(fromPrev, "ci", "FromPrevHoldNewSendOldNode", tr),
		ToNext: Wiring.OutMulti{
			Wiring.NewOut(out0, "ci", "ToNext", tr),
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()
	defer func() { cancel(); wg.Wait() }()

	// First recv: Held is -1, so ToNext must emit NOTHING.
	fromPrev <- 0
	select {
	case v := <-out0:
		t.Fatalf("first fire emitted %d on ToNext; expected nothing (Held was -1)", v)
	case <-time.After(60 * time.Millisecond):
		// no bead: correct.
	}

	// Held is now 0. Next recv forwards the real held value 0.
	fromPrev <- 1
	if got := recv(t, out0); got != 0 {
		t.Fatalf("second fire ToNext: got %d, want 0", got)
	}
}
