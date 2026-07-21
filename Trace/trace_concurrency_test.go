package Trace

import (
	"bytes"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// trace_concurrency_test.go — pins the NEW invariant that replaced Trace.mu (see
// the wait-for-everything-then-close change): events/sink/onEvent/debugSink
// have exactly ONE writer, the drain goroutine — every producer goroutine (node
// Update loops, movers, Breadcrumb callers) only ever sends on t.ch via emit(). No
// lock is needed because nothing outside drain ever touches those fields while the
// trace is live; Events()/WriteJSONL() read events only after Close() (documented on
// the Trace struct), by which point drain has exited.
//
// TestTraceConcurrentEmitVsClose drives many goroutines calling Emit in a tight loop
// against one goroutine calling Close, under `go test -race`, for long enough to
// catch a would-be reintroduced direct write to events/sink from a caller goroutine
// (that is exactly the shape TestBreadcrumbWritesToDebugSink's deliberate-break
// red-proof exercised on Breadcrumb specifically; this test exercises Emit). It also
// checks the doc comment's specific claim that "ch is NEVER closed": a send on a
// closed channel panics, so if that claim were false (t.ch were ever closed while an
// emit is mid-flight), a concurrent emitter would panic. This test recovers any
// goroutine panic and fails if one occurs.
func TestTraceConcurrentEmitVsClose(t *testing.T) {
	tr := New(8)

	var wg sync.WaitGroup
	var panicked atomic.Bool

	const emitters = 16
	for i := 0; i < emitters; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					panicked.Store(true)
				}
			}()
			for j := 0; j < 2000; j++ {
				tr.Emit(Event{Kind: KindRecv, Node: "n", Port: "p", Value: j})
			}
		}(i)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		tr.Close()
	}()

	wg.Wait()

	if panicked.Load() {
		t.Fatalf("a concurrent Emit panicked racing Close() — violates the \"ch is NEVER closed\" claim")
	}
}

// TestTraceBreadcrumbConcurrentWithClose checks that Breadcrumb — an ordinary event
// routed through emit()/t.ch like Recv/Fire/Send, written by the drain goroutine's
// writeBreadcrumb instead of writing sink/debugSink directly on the caller's own
// goroutine — does not race Close's teardown, and never panics (a send on t.ch
// racing Close must select the stopped case, per emit's contract, not block or
// panic).
func TestTraceBreadcrumbConcurrentWithClose(t *testing.T) {
	var buf bytes.Buffer
	tr := NewWithSink(8, &buf)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				tr.Breadcrumb("label", "node", "port", "value")
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		tr.Close()
	}()
	wg.Wait()
}

// TestNoGoroutineOutlivesClose proves the second half of the invariant that let
// Trace.mu go: Close() does not return until the drain goroutine has itself
// observed t.stopped and exited (drain.go: Close's closeOnce.Do body ends with
// `<-t.done`, and drain closes t.done as its very last act). So by the time Close()
// returns, no goroutine belonging to this Trace should still be alive. We drive a
// burst of concurrent Emit callers first so the drain goroutine is demonstrably
// doing real work (not idle) at the moment Close is called, then assert the
// runtime's live goroutine count has returned to (at most) its pre-Trace baseline.
func TestNoGoroutineOutlivesClose(t *testing.T) {
	// Let any goroutines from earlier subtests/GC bookkeeping settle before taking
	// the baseline, so this test isn't flaky on unrelated leftover goroutines.
	runtime.Gosched()
	baseline := runtime.NumGoroutine()

	tr := New(8)

	var wg sync.WaitGroup
	const emitters = 8
	for i := 0; i < emitters; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				tr.Emit(Event{Kind: KindRecv, Node: "n", Value: j})
			}
		}(i)
	}
	wg.Wait()

	tr.Close()

	// Close() already blocked until drain exited, so this should be true
	// immediately — the retry loop only absorbs runtime bookkeeping jitter in
	// NumGoroutine itself, not a real wait for drain.
	deadline := time.Now().Add(2 * time.Second)
	for {
		after := runtime.NumGoroutine()
		if after <= baseline {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutine(s) outlived Close(): baseline=%d after=%d", baseline, after)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
