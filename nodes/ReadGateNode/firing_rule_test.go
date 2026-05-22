package ReadGateNode

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

// FiresWhenBothPresent: value from FromInput is forwarded on ToChainInhibitor
// when FromChainInhibitor also arrives; inhibitor value is ignored.
func TestFiresWhenBothPresent(t *testing.T) {
	tr := T.New(0)
	defer tr.Close()
	fromInput := make(chan int, 1)
	fromCI := make(chan int, 1)
	toCI := make(chan int, 1)

	node := &ReadGateNode{
		Fire:               func() { tr.Fire("rg") },
		FromInput:          Wiring.NewIn(fromInput, "rg", "FromInput", tr),
		FromChainInhibitor: Wiring.NewIn(fromCI, "rg", "FromChainInhibitor", tr),
		ToChainInhibitor:   Wiring.NewOut(toCI, "rg", "ToChainInhibitor", tr),
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	fromInput <- 42
	fromCI <- 1
	got := recv(t, toCI)
	cancel()
	wg.Wait()

	if got != 42 {
		t.Fatalf("expected 42, got %d", got)
	}
}

// NoFireWithoutInhibitor: value alone must not emit.
func TestNoFireWithoutInhibitor(t *testing.T) {
	tr := T.New(0)
	defer tr.Close()
	fromInput := make(chan int, 1)
	fromCI := make(chan int, 1)
	toCI := make(chan int, 1)

	node := &ReadGateNode{
		Fire:               func() { tr.Fire("rg") },
		FromInput:          Wiring.NewIn(fromInput, "rg", "FromInput", tr),
		FromChainInhibitor: Wiring.NewIn(fromCI, "rg", "FromChainInhibitor", tr),
		ToChainInhibitor:   Wiring.NewOut(toCI, "rg", "ToChainInhibitor", tr),
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	fromInput <- 7
	time.Sleep(20 * time.Millisecond)
	cancel()
	wg.Wait()

	select {
	case v := <-toCI:
		t.Fatalf("unexpected emission %d", v)
	default:
	}
}
