package Wiring

import (
	"context"
	"sync"
	"testing"
	"time"
)

// node_mover_geom_race_test.go — regression check that MoveDispatch.NodeKind stays
// lock-free and never reaches back into a mover's live geom. NodeKind, called from a
// goroutine OTHER than the mover's own inbox-drain goroutine, reads the immutable
// md.kinds table (built once at newMoveDispatch construction, never mutated), while
// applyCenter (driven from the mover's own goroutine via RootMove/moveMsgKindCenter)
// concurrently writes nm.geom's MUTABLE ScenePolar/HasPos/ReachR fields. The two touch
// DISJOINT memory — an immutable table vs. a single-goroutine-confined struct — so this
// drives both concurrently under -race and it must stay clean. If a future change ever
// routed NodeKind back through nm.geom (e.g. reading nm.geom.Kind directly again),
// this test would start reporting a real DATA RACE.
func TestNodeKindConcurrentWithApplyCenterUnderRace(t *testing.T) {
	root := writeTree(t)
	md := loadTreeMD(t, root)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	md.Start(ctx)

	srcCenter, ok := md.centerOfNode("src")
	if !ok {
		t.Fatal("no center for src")
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Reader goroutine: NOT the mover's own goroutine. Hammers NodeKind in a tight loop
	// for a bounded deadline (not a fixed sleep) — mirrors this package's
	// deadline-bounded poll convention (pollDragConverged et al.).
	wg.Add(1)
	go func() {
		defer wg.Done()
		deadline := time.Now().Add(500 * time.Millisecond)
		for time.Now().Before(deadline) {
			select {
			case <-stop:
				return
			default:
			}
			if k := md.NodeKind("src"); k == "" {
				t.Errorf("NodeKind(src) returned empty; want %q", "SrcNode")
			}
		}
	}()

	// Writer: repeatedly drive RootMove for "src" from THIS goroutine (also not the
	// mover's own goroutine — RootMove enqueues onto the mover's inbox; the mover's own
	// run/handle goroutine is the one that actually calls applyCenter). This keeps
	// applyCenter's writes to m.geom actively in flight while the reader above hammers
	// NodeKind, for as long as the reader is still running.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(stop)
		deadline := time.Now().Add(500 * time.Millisecond)
		i := 0
		for time.Now().Before(deadline) {
			i++
			target := srcCenter.add(vec3{X: float64(i%7) - 3, Y: float64(i%5) - 2, Z: float64(i%3) - 1})
			md.RootMove("src", target)
		}
	}()

	wg.Wait()
}
