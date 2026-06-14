package Trace

import (
	"sync"
	"testing"
)

// TestCloseRace hammers concurrent emits while Close() tears the trace down.
// Before the fix, Close() did close(t.ch) and a concurrent sender doing
// t.ch <- e would panic with "send on closed channel". With the stop-signal
// design t.ch is never closed, so the in-flight send selects the stopped case
// and drops silently. Run under -race; the assertion is simply NO panic.
func TestCloseRace(t *testing.T) {
	const (
		buf        = 8 // small buffer => emits actually block/contend with Close
		goroutines = 32
		iters      = 2000
	)
	tr := New(buf)

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				tr.Position("node", "out", i, 1, 2, 3, 0.5, uint64(i))
				tr.Emit(Event{Kind: KindFire, Node: "node"})
				tr.Send("node", "out", i)
			}
		}()
	}

	// Close concurrently with the emit storm.
	closeDone := make(chan struct{})
	go func() {
		tr.Close()
		close(closeDone)
	}()

	wg.Wait()
	<-closeDone

	// Close is idempotent and must not panic on a second call either.
	tr.Close()
}
