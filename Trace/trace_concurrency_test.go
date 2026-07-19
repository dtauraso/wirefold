package Trace

import (
	"bytes"
	"sync"
	"sync/atomic"
	"testing"
)

// trace_concurrency_test.go — pins Trace.mu's actual contention claim: every
// node goroutine calls Emit (via emit) concurrently, the drain goroutine appends
// to t.events and writes t.sink under the lock, and Close() flips t.closed /
// closes t.stopped. This is many emitters + one drain + one closer, all touching
// events/closed/sink/onEvent/debugSink.
//
// TestTraceConcurrentEmitVsClose drives many goroutines calling Emit in a tight
// loop against one goroutine calling Close, under `go test -race`, for long
// enough to catch an unsynchronized read/write if Trace.mu is removed from the
// touched methods (Events/SetDebugSink/Breadcrumb/drain's record). It is the
// falsifiability proof for this guard.
//
// It also checks the doc comment's specific claim that "ch is NEVER closed" and
// Close signals via a separate stopped channel: a send on a closed channel
// panics, so if that claim were false (t.ch were ever closed while an emit is
// mid-flight), a concurrent emitter would panic. TestTraceConcurrentEmitVsClose
// recovers any goroutine panic and fails the test if one occurs.
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

	// Reader goroutines exercise Events()/SetDebugSink() concurrently with the
	// emitters and the closer, touching the same mu-guarded fields from a third
	// angle.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				_ = tr.Events()
				tr.SetDebugSink(nil)
			}
		}()
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

// TestTraceBreadcrumbConcurrentWithClose checks that Breadcrumb (which writes
// directly to sink/debugSink under mu, independent of the emit/ch path) does not
// race Close's mutation of t.closed/t.stopped.
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
