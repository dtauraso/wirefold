package ChainInhibitorNode

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

// On receive, emit Held to every ToNext entry, then store the new value.
func TestFireOnReceive(t *testing.T) {
	tr := T.New(0)
	defer tr.Close()
	fromPrev := make(chan int, 1)
	out0 := make(chan int, 1)
	out1 := make(chan int, 1)

	node := &ChainInhibitorNode{
		Fire:                       func() { tr.Fire("ci") },
		Held:                       99,
		FromPrevChainInhibitorNode: Wiring.NewIn(fromPrev, "ci", "FromPrevChainInhibitorNode", tr),
		ToNext: Wiring.OutMulti{
			Wiring.NewOut(out0, "ci", "ToNext", tr),
			Wiring.NewOut(out1, "ci", "ToNext", tr),
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	fromPrev <- 7
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
