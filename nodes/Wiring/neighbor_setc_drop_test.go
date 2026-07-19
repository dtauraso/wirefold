// neighbor_setc_drop_test.go — investigates whether requantizeLocalPolars' sendMoveLossy
// fan of moveMsgKindNeighborSetC (quantized_move.go) EVER actually drops in practice, and
// (if so) whether routing it through the sender's own outbox instead removes the drops
// without reintroducing the mutual-adjacency cascade deadlock that TestMutuallyAdjacentDragFloodNoDeadlock
// guards. See task write-up for the full investigation; this file only proves/disproves
// reachability of the "requantize.drop" breadcrumb under realistic concurrent-drag
// pressure, using the SAME mutually-adjacent flood shape as the deadlock test (that test
// is the highest-pressure repro already known to exist in this package).
package Wiring

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
)

// lockedWriter serializes concurrent writes to an underlying bytes.Buffer: multiple
// nodeMover goroutines call tr.Breadcrumb concurrently, and Trace's debug-sink write is
// a direct io.Writer.Write with no internal lock of its own (see Trace.go), so the sink
// itself must serialize.
type lockedWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *lockedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// loadTreeMDWithDebugSink is loadTreeMD but with a caller-supplied debug sink so the test
// can count "requantize.drop" breadcrumbs, which loadTreeMD's private tr.New(0) (no sink)
// makes unobservable.
func loadTreeMDWithDebugSink(t *testing.T, root string, dbg *lockedWriter) *MoveDispatch {
	t.Helper()
	tr := T.New(0)
	tr.SetDebugSink(dbg)
	_, _, md, err := LoadTopology(context.Background(), root, tr, NewRealClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}
	return md
}

// TestNeighborSetCDropReachability drives the same mutually-adjacent concurrent-drag flood
// as TestMutuallyAdjacentDragFloodNoDeadlock (the known highest-pressure repro in this
// package) and counts "requantize.drop" breadcrumbs emitted by sendMoveLossy
// (node_move.go). This is step 1 of the investigation: establish whether the drop path in
// requantizeLocalPolars' neighborSetC fan is reachable at all before touching any
// behavior. A zero count here is itself the finding, not a test bug.
func TestNeighborSetCDropReachability(t *testing.T) {
	root := writeTree(t)
	dbg := &lockedWriter{}
	md := loadTreeMDWithDebugSink(t, root, dbg)
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
	case <-time.After(30 * time.Second):
		t.Fatal("TestNeighborSetCDropReachability: flood did not complete in 30s")
	}

	// Give the inbox-drain goroutines a moment to finish processing whatever is still
	// queued (RootMove returning only means the drag message was accepted into the
	// dispatch channel, not that its handle()/requantize has finished running).
	time.Sleep(200 * time.Millisecond)

	out := dbg.String()
	drops := strings.Count(out, `"label":"requantize.drop"`)
	t.Logf("requantize.drop count under mutually-adjacent flood (%d workers x %d moves x 2 nodes = %d RootMove calls): %d",
		workers, perWorker, workers*perWorker*2, drops)
}
