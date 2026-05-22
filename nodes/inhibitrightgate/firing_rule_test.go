package inhibitrightgate

import (
	"context"
	"sync"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
	"github.com/dtauraso/wirefold/nodes/Wiring"
)

func run(left, right int) (int, error) {
	tr := T.New(0)
	defer tr.Close()
	fromLeft := make(chan int, 1)
	fromRight := make(chan int, 1)
	toPassed := make(chan int, 1)
	node := &Node{
		Fire:      func() { tr.Fire("irg") },
		FromLeft:  Wiring.NewIn(fromLeft, "irg", "FromLeft", tr),
		FromRight: Wiring.NewIn(fromRight, "irg", "FromRight", tr),
		ToPassed:  Wiring.NewOut(toPassed, "irg", "ToPassed", tr),
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()
	fromLeft <- left
	fromRight <- right
	select {
	case v := <-toPassed:
		cancel()
		wg.Wait()
		return v, nil
	case <-time.After(100 * time.Millisecond):
		cancel()
		wg.Wait()
		return -1, nil
	}
}

// left=1, right=0 → passes (1).
func TestPassWhenLeftOnlyActive(t *testing.T) {
	got, _ := run(1, 0)
	if got != 1 {
		t.Fatalf("expected 1, got %d", got)
	}
}

// left=1, right=1 → inhibited (0).
func TestInhibitedWhenRightActive(t *testing.T) {
	got, _ := run(1, 1)
	if got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}

// left=0, right=0 → 0.
func TestZeroWhenLeftInactive(t *testing.T) {
	got, _ := run(0, 0)
	if got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}
