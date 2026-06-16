package input

import (
	"context"
	"sync"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
	"github.com/dtauraso/wirefold/nodes/Wiring"
)

// beadSnapshot is one EmitNodeBeads call captured by the test stub.
type beadSnapshot struct {
	working []int
	backup  []int
}

// TestNodeBeadSnapshotsTrackArray drives the plain-emit path and asserts that
// EmitNodeBeads is called with the LIVE working/backup arrays on the initial
// state and after every pop/refill, so the emitted set always reflects the buffer:
// 4 beads when full (2 rows of 2), one fewer right after a pop, back to 4 after a
// refill. Init=[1,0] → working=[1,0], backup=[1,0].
func TestNodeBeadSnapshotsTrackArray(t *testing.T) {
	tr := T.New(0)
	defer tr.Close()
	toRG := make(chan int, 16)

	var mu sync.Mutex
	var snaps []beadSnapshot

	node := &Node{
		Fire:       func() { tr.Fire("in") },
		Init:       []int{1, 0},
		Repeat:     false, // one working drain: 2 pops then exit
		ToHoldNewSendOld: Wiring.NewOut(toRG, "in", "ToHoldNewSendOld", tr),
		EmitNodeBeads: func(working, backup []int) {
			mu.Lock()
			snaps = append(snaps, beadSnapshot{
				working: append([]int(nil), working...),
				backup:  append([]int(nil), backup...),
			})
			mu.Unlock()
		},
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
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Update did not finish")
	}

	mu.Lock()
	defer mu.Unlock()

	// Snapshots, in order, with bead counts (len(working)+len(backup)):
	//   initial    : working=[1,0] backup=[1,0] → 4
	//   after pop 1 : working=[1]   backup=[1,0] → 3 (popped 0)
	//   after pop 2 : working refilled = backup [1,0], backup=[1,0] → 4
	if len(snaps) != 3 {
		t.Fatalf("got %d snapshots, want 3: %+v", len(snaps), snaps)
	}
	counts := make([]int, len(snaps))
	for i, s := range snaps {
		counts[i] = len(s.working) + len(s.backup)
	}
	wantCounts := []int{4, 3, 4}
	for i, w := range wantCounts {
		if counts[i] != w {
			t.Errorf("snapshot %d: %d beads, want %d (%+v)", i, counts[i], w, snaps[i])
		}
	}

	// Initial full state: both rows [1,0].
	if got := snaps[0]; len(got.working) != 2 || len(got.backup) != 2 ||
		got.working[0] != 1 || got.working[1] != 0 || got.backup[0] != 1 || got.backup[1] != 0 {
		t.Errorf("initial snapshot = %+v, want working=[1,0] backup=[1,0]", got)
	}
	// After the first pop (end of [1,0] = 0), working=[1].
	if got := snaps[1]; len(got.working) != 1 || got.working[0] != 1 {
		t.Errorf("after pop 1: working = %v, want [1]", got.working)
	}
	// After the second pop working empties and refills from backup: working=[1,0].
	if got := snaps[2]; len(got.working) != 2 || got.working[0] != 1 || got.working[1] != 0 {
		t.Errorf("after refill: working = %v, want [1,0]", got.working)
	}
}
