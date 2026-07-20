// outbox_mutual_adjacency_test.go — CHECKS BY CODE the claims
// docs/planning/visual-editor/outbox-two-channels.md makes about the two-channels,
// no-inbox(-shared-queue), no-blocking mover restructure:
//
//  1. The restructure is load-bearing against the cascade deadlock described in the
//     (branch-local, since-stripped) cascade-deadlock-fix.md: two mutually-adjacent
//     nodeMovers, both mid-handle (commitNodeMoveLocal -> fanEdgesAndPartners), each
//     fanning to the other under sustained concurrent drag load, must never hang.
//  2. Nothing a nodeMover sends is ever dropped, and per-destination delivery order is
//     exactly enqueue order (nm.pending's retain-and-retry is FIFO per destination),
//     INCLUDING when the destination's inbox is genuinely full and the retry path must
//     fire.
//  3. Starting a mover spawns exactly one goroutine (its own run loop) — no dedicated
//     sender goroutine and no ctx-cancel watcher goroutine alongside it.
package Wiring

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"
)

// TestMutuallyAdjacentDragFloodNoDeadlock drives the real production path end to end:
// two mutually-adjacent nodes (src/dst over edge e0, the writeTree fixture), each
// hammered with concurrent RootMove drags. Every commit's handle() runs
// commitNodeMoveLocal -> fanEdgesAndPartners, which sends a partner re-emit via the
// COMMITTING node's own nm.sendMove (== md.enqueueFor(nm), wired at newMoveDispatch).
// Under load this floods both directions' pending-retry queues and both nodes' inboxes
// concurrently -- the exact "both mid-handle, each fanning to the other" condition. If a
// send ever blocked (handle calling a raw blocking channel write instead of the
// non-blocking retain-and-retry send), this test hangs and the outer `go test -timeout`
// kills the run; the inner select below turns that into a clean t.Fatal instead of a
// bare hang when run without an outer timeout.
//
// This is a MANDATORY RED PROOF: temporarily rewiring nm.sendMove to the old blocking
// md.sendMove (bypassing the non-blocking retry queue) makes this same test hang and
// fail on timeout every time; restoring the non-blocking wiring makes it pass again
// (confirmed by hand during this restructure — see the subagent report).
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
	case <-time.After(60 * time.Second):
		// 60s, not 10s: RootMove's send into the dragged node's OWN inbox
		// (md.sendMove) is a genuinely BOUNDED, blocking-with-ctx-escape send (buffer
		// 8) under this restructure -- unlike the old unbounded outbox, which let a
		// worker's RootMove return the instant it enqueued (real processing happened
		// off the critical path), a worker here is throttled by how fast its target
		// node actually drains its inbox. That is the deliberate bounded-channel
		// trade the design accepts (docs/planning/visual-editor/
		// outbox-two-channels.md); under -race (measured ~12-18x slower than a plain
		// build for this same load) the wall-clock margin needs to be generous, not
		// tight. This deadline still catches a genuine hang -- it does not need to be
		// unbounded to do that.
		t.Fatal("TestMutuallyAdjacentDragFloodNoDeadlock: timed out -- mutually adjacent " +
			"nodes' concurrent commits deadlocked (a send blocked instead of retain-and-retry)")
	}
}

// TestOutboxFIFOPerTargetOrderNoDrop drives nm.pending's retain-and-retry send directly
// (the same mechanism nm.sendMove/flushPending implement): a single sender goroutine
// (mirroring the real shape -- one handler goroutine, its own run loop, doing every send
// for that mover) interleaves many sequenced items across several destination ids, while
// the destinations' inbox channels are deliberately tiny (buffered 1) and drained slowly
// from separate goroutines -- forcing flushPending's retain-on-full-channel path
// explicitly, not merely hoping a big buffer never fills. Asserts every item arrives (no
// drops) and each destination's own subsequence arrives in exactly send order (FIFO per
// target, even across retries).
func TestOutboxFIFOPerTargetOrderNoDrop(t *testing.T) {
	const totalCount = 4000
	dests := []string{"A", "B", "C"}

	chans := map[string]chan moveMsg{}
	for _, d := range dests {
		// Buffered 1 (not "big enough to never fill"): the sender will routinely
		// outrun a deliberately-throttled receiver, forcing flushPending's retain
		// path on every destination, repeatedly.
		chans[d] = make(chan moveMsg, 1)
	}

	nm := &nodeMover{
		resolveDest: func(id string) (chan moveMsg, bool) {
			ch, ok := chans[id]
			return ch, ok
		},
	}

	var mu sync.Mutex
	receivedByTarget := map[string][]int{}
	receivedTotal := 0
	done := make(chan struct{})
	var doneOnce sync.Once

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, d := range dests {
		go func(dest string, ch chan moveMsg) {
			for {
				select {
				case msg := <-ch:
					// Deliberately slow receiver: this is what keeps the tiny
					// buffered-1 channel full most of the time, forcing the
					// sender's retain-and-retry path rather than a single lucky
					// non-blocking send per item.
					time.Sleep(50 * time.Microsecond)
					mu.Lock()
					receivedByTarget[dest] = append(receivedByTarget[dest], msg.AnchorId)
					receivedTotal++
					if receivedTotal == totalCount {
						doneOnce.Do(func() { close(done) })
					}
					mu.Unlock()
				case <-ctx.Done():
					return
				}
			}
		}(d, chans[d])
	}

	// Single sender goroutine (matches production: one handler goroutine's
	// fanEdgesAndPartners/requantizeLocalPolars call sends every outbound message for
	// that mover sequentially, via nm.sendMove == md.enqueueFor(nm)). AnchorId carries
	// the send sequence number so delivery order is directly checkable. This is the
	// same append-then-flush nm.sendMove performs, plus nm.run's own retry-loop call
	// (flushPending on its own, with no new item) standing in for the mover's
	// per-cycle retry.
	go func() {
		for i := 0; i < totalCount; i++ {
			dest := dests[i%len(dests)]
			nm.pending = append(nm.pending, pendingSend{destID: dest, msg: moveMsg{AnchorId: i}})
			nm.flushPending()
		}
		// Keep retrying whatever didn't fit until it all drains (mirrors nm.run's
		// per-cycle flushPending call).
		for {
			mu.Lock()
			left := receivedTotal
			mu.Unlock()
			if left >= totalCount {
				return
			}
			nm.flushPending()
			time.Sleep(time.Millisecond)
		}
	}()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
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
		t.Fatalf("dropped items: delivered %d, sent %d", gotTotal, totalCount)
	}
}

// TestNodeMoverGoroutineCountDropsByTwo asserts (rather than assumes) that starting the
// movers for the writeTree fixture (2 nodes, 1 edge) spawns exactly 3 goroutines — one
// per mover, no dedicated sender goroutine and no ctx-cancel watcher goroutine alongside
// each node mover (the two goroutines per node mover docs/planning/visual-editor/
// outbox-two-channels.md says must go away).
func TestNodeMoverGoroutineCountDropsByTwo(t *testing.T) {
	root := writeTree(t)
	md := loadTreeMD(t, root)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	before := stableGoroutineCount(t)
	md.Start(ctx)
	// Let every goroutine actually start running (go statements return immediately).
	after := stableGoroutineCount(t)

	wantMovers := len(md.nodeMovers) + len(md.edgeMovers)
	gotDelta := after - before
	if gotDelta != wantMovers {
		t.Fatalf("goroutine count after Start: delta=%d, want exactly %d (one goroutine per mover: "+
			"%d nodeMovers + %d edgeMovers, no dedicated sender/watcher goroutines)",
			gotDelta, wantMovers, len(md.nodeMovers), len(md.edgeMovers))
	}
}

// stableGoroutineCount samples runtime.NumGoroutine() until it stabilizes (a few GC/
// scheduler housekeeping goroutines can transiently appear/disappear), so the comparison
// in TestNodeMoverGoroutineCountDropsByTwo isn't flaky on background noise.
func stableGoroutineCount(t *testing.T) int {
	t.Helper()
	runtime.Gosched()
	last := runtime.NumGoroutine()
	for i := 0; i < 20; i++ {
		time.Sleep(5 * time.Millisecond)
		n := runtime.NumGoroutine()
		if n == last {
			return n
		}
		last = n
	}
	return last
}
