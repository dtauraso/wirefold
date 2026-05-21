package InhibitRightGateNode

import (
	"context"
	"sync"
	"testing"
	"time"

	S "github.com/dtauraso/wirefold/nodes/SafeWorker"
)

// run sends left and right into the node and waits for it to consume both.
// Returns true if the node fired (consumed both inputs) within the timeout.
func run(left, right int) bool {
	fromLeft := make(chan int, 1)
	fromRight := make(chan int, 1)
	node := &InhibitRightGateNode{
		Name:      "irg",
		FromLeft:  fromLeft,
		FromRight: fromRight,
	}
	ctx, cancel := context.WithCancel(context.Background())
	wg := &sync.WaitGroup{}
	wg.Add(1)
	sw := &S.SafeWorker{Ctx: ctx, Wg: wg, Trace: nil}
	go node.Update(sw)
	fromLeft <- left
	fromRight <- right
	// Give the node time to consume both inputs and fire.
	time.Sleep(50 * time.Millisecond)
	fired := !node.HasLeft && !node.HasRight
	cancel()
	wg.Wait()
	return fired
}

// Node should consume both inputs regardless of values.
func TestFiresWhenBothInputsPresent(t *testing.T) {
	if !run(1, 0) {
		t.Fatal("expected node to fire (consume both inputs)")
	}
}

func TestFiresWhenBothInhibited(t *testing.T) {
	if !run(1, 1) {
		t.Fatal("expected node to fire (consume both inputs)")
	}
}

func TestFiresWhenBothZero(t *testing.T) {
	if !run(0, 0) {
		t.Fatal("expected node to fire (consume both inputs)")
	}
}
