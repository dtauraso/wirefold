package input

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

// Emits each Init value in order on ToReadGate then exits.
func TestEmitsInitValues(t *testing.T) {
	tr := T.New(0)
	defer tr.Close()
	toRG := make(chan int, 3)
	node := &Node{
		Fire:       func() { tr.Fire("in") },
		Init:       []int{10, 20, 30},
		ToReadGate: Wiring.NewOut(toRG, "in", "ToReadGate", tr),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	// Update exits after all Init values are sent, so wg.Wait suffices.
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("InputNode did not finish sending Init values in time")
	}

	want := []int{10, 20, 30}
	for i, w := range want {
		got := recv(t, toRG)
		if got != w {
			t.Errorf("value[%d]: expected %d, got %d", i, w, got)
		}
	}
}

// Empty Init: Update returns without emitting anything.
func TestEmptyInit(t *testing.T) {
	tr := T.New(0)
	defer tr.Close()
	toRG := make(chan int, 1)
	node := &Node{
		Fire:       func() { tr.Fire("in") },
		Init:       nil,
		ToReadGate: Wiring.NewOut(toRG, "in", "ToReadGate", tr),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); node.Update(ctx) }()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout: InputNode with empty Init should exit immediately")
	}

	select {
	case v := <-toRG:
		t.Fatalf("unexpected emission %d", v)
	default:
	}
}
