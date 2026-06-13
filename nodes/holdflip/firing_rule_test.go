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
