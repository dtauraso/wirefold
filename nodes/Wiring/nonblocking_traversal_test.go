// nonblocking_traversal_test.go — proves beads still traverse correctly
// through a real node goroutine (Input -> HoldNewSendOld) after the node
// receive/send loops were converted to non-blocking polling
// (task/non-blocking-update). Drives real node goroutines via the loader,
// not mocks. External test package so it can import the concrete node kinds
// (which import Wiring) without an import cycle.
package Wiring_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
	W "github.com/dtauraso/wirefold/nodes/Wiring"
	_ "github.com/dtauraso/wirefold/nodes/holdnewsendold"
	_ "github.com/dtauraso/wirefold/nodes/input"
)

// TestInputToHoldNewSendOldTraversal drives a real Input node (init=[1,0],
// repeat=false) wired to a real HoldNewSendOld node and asserts the bead
// values are received in order on the destination port. This exercises both
// the converted non-blocking RECEIVE loop (holdnewsendold reads
// FromPrevHoldNewSendOldNode via PollRecv) and confirms the wire's own
// PacedWire delivery pacing is unchanged.
func TestInputToHoldNewSendOldTraversal(t *testing.T) {
	const topo = `{
	  "nodes": [
	    {"id":"src","type":"Input","data":{"init":[1,0],"repeat":false},
	     "outputs":[{"name":"ToHoldNewSendOld"}]},
	    {"id":"dst","type":"HoldNewSendOld","data":{"state":{"held":-1}},
	     "inputs":[{"name":"FromPrevHoldNewSendOldNode"}]}
	  ],
	  "edges": [
	    {"label":"e1","kind":"data","source":"src","sourceHandle":"ToHoldNewSendOld","target":"dst","targetHandle":"FromPrevHoldNewSendOldNode"}
	  ]
	}`

	dir := t.TempDir()
	path := filepath.Join(dir, "topo.json")
	if err := os.WriteFile(path, []byte(topo), 0o600); err != nil {
		t.Fatal(err)
	}

	tr, live := newTraceWithLiveEvents(256)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nodes, _, nmr, _, err := W.LoadTopology(ctx, path, tr, W.NewRealClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}
	nmr.Start(ctx)

	for _, n := range nodes {
		go n.Update(ctx)
	}

	// Wait for both bead values (1 then 0, per popEnd end-pop order) to be
	// received on dst.FromPrevHoldNewSendOldNode.
	deadline := time.After(5 * time.Second)
	var got []int
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for len(got) < 2 {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for 2 recv events on dst; got %v so far", got)
		case <-poll.C:
			got = got[:0]
			for _, e := range live.snapshot() {
				if e.Kind == T.KindRecv && e.Node == "dst" && e.Port == "FromPrevHoldNewSendOldNode" {
					got = append(got, e.Value)
				}
			}
		}
	}

	// popEnd pops the END of working ([1,0]) first, so 0 arrives before 1.
	if len(got) < 2 || got[0] != 0 || got[1] != 1 {
		t.Fatalf("dst recv sequence = %v, want [0 1 ...]", got)
	}
}
