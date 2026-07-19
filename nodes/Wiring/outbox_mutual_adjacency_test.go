// outbox_mutual_adjacency_test.go — CHECKS BY CODE the two claims outbox's doc comment
// (node_mover.go) makes about outbox.mu / the outbox type:
//
//  1. The unbounded FIFO is load-bearing against the cascade deadlock described in the
//     (branch-local, since-stripped) cascade-deadlock-fix.md: two mutually-adjacent
//     nodeMovers, both mid-handle (commitNodeMoveLocal -> fanEdgesAndPartners), each
//     fanning to the other under sustained concurrent drag load, must never hang.
//  2. Nothing enqueued onto an outbox is ever dropped, and per-target delivery order is
//     exactly enqueue order (a single dedicated sender goroutine pops the FIFO).
//
// No prior test in this package exercised either claim (grepped: no outbox/cascade/
// mutual-adjacency test existed; the ORIGINAL TestMutuallyAdjacentCascadeNoDeadlock from
// commit dbd68d77 targeted fanEdgesAndPartners/handleTrigger call sites that were later
// deleted wholesale by the neighborSetC rewrite (b1620186) — its test file
// (node5_equalize_test.go) no longer exists). These are NEW tests, not a strengthening of
// an existing one.
package Wiring

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestMutuallyAdjacentDragFloodNoDeadlock drives the real production path end to end:
// two mutually-adjacent nodes (src/dst over edge e0, the writeTree fixture), each
// hammered with concurrent RootMove drags. Every commit's handle() runs
// commitNodeMoveLocal -> fanEdgesAndPartners, which enqueues a partner re-emit onto the
// COMMITTING node's own outbox (nm.sendMove == md.enqueueFor(nm.outbox), wired at
// newMoveDispatch). Under load this floods both directions' outboxes and both nodes'
// inboxes concurrently -- the exact "both mid-handle, each fanning to the other"
// condition the doc comment names. If the outbox's enqueue ever blocked (a bounded queue,
// or handle sending directly instead of enqueueing), this test hangs and the outer
// `go test -timeout` kills the run; the inner select below turns that into a clean
// t.Fatal instead of a bare hang when run without an outer timeout.
func TestMutuallyAdjacentDragFloodNoDeadlock(t *testing.T) {
	root := writeTree(t)
	md := loadTreeMD(t, root)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	const workers = 12
	const perWorker = 400

	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(2)
			go func(seed int) {
				defer wg.Done()
				for i := 0; i < perWorker; i++ {
					md.RootMove("src", vec3{X: float64(seed%7) - 3, Y: float64(i%5) - 2, Z: float64(i % 3)})
				}
			}(w)
			go func(seed int) {
				defer wg.Done()
				for i := 0; i < perWorker; i++ {
					md.RootMove("dst", vec3{X: float64(i%5) - 2, Y: float64(seed%7) - 3, Z: float64(i % 3)})
				}
			}(w)
		}
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All workers' RootMove calls returned and every commit's fan-out drained --
		// no mutual-adjacency deadlock.
	case <-time.After(10 * time.Second):
		t.Fatal("TestMutuallyAdjacentDragFloodNoDeadlock: timed out -- mutually adjacent " +
			"nodes' concurrent commits deadlocked (outbox no longer decouples handle from " +
			"the blocking delivery send)")
	}
}

// TestOutboxFIFOPerTargetOrderNoDrop drives the outbox primitive directly (the same type
// node_mover.go's outbox.mu guards): a single enqueuer goroutine (mirroring the real
// shape -- one handler goroutine, its own inbox-drain, doing every enqueue for that
// mover) interleaves many sequenced items across two destination ids, while the
// dedicated sender goroutine (outbox.run) delivers them. Asserts every item arrives
// (no drops) and each destination's own subsequence arrives in exactly enqueue order
// (FIFO per target).
func TestOutboxFIFOPerTargetOrderNoDrop(t *testing.T) {
	ob := newOutbox()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const totalCount = 4000
	var mu sync.Mutex
	receivedByTarget := map[string][]int{}
	receivedTotal := 0
	done := make(chan struct{})

	go ob.run(ctx, func(destID string, msg moveMsg) {
		mu.Lock()
		receivedByTarget[destID] = append(receivedByTarget[destID], msg.AnchorId)
		receivedTotal++
		if receivedTotal == totalCount {
			close(done)
		}
		mu.Unlock()
	})

	// Single enqueuer (matches production: one handler goroutine's fanEdgesAndPartners
	// call enqueues every outbound message for that mover sequentially). AnchorId
	// carries the enqueue sequence number so delivery order is directly checkable.
	for i := 0; i < totalCount; i++ {
		dest := "A"
		if i%3 == 0 {
			dest = "B"
		} else if i%5 == 0 {
			dest = "C"
		}
		ob.enqueue(dest, moveMsg{AnchorId: i})
	}

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		mu.Lock()
		got := receivedTotal
		mu.Unlock()
		t.Fatalf("TestOutboxFIFOPerTargetOrderNoDrop: timed out waiting for delivery -- "+
			"got %d/%d (dropped or stuck)", got, totalCount)
	}

	mu.Lock()
	defer mu.Unlock()
	gotTotal := 0
	for dest, seq := range receivedByTarget {
		gotTotal += len(seq)
		for i := 1; i < len(seq); i++ {
			if seq[i] <= seq[i-1] {
				t.Fatalf("target %q: out-of-order delivery at index %d: %v", dest, i, seq)
			}
		}
	}
	if gotTotal != totalCount {
		t.Fatalf("dropped items: delivered %d, enqueued %d", gotTotal, totalCount)
	}
}
