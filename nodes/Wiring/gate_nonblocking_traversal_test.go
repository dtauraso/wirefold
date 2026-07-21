// gate_nonblocking_traversal_test.go — proves a WindowAndInhibitLeftGate still
// fires and its output bead still traverses through a real paced wire after
// gatecommon.RunGate's output drive was converted from a blocking
// EmitOneDriven to place-then-StepOnce-per-cycle (task/non-blocking-update,
// piece 6). Drives real node goroutines via the loader, not mocks. External
// test package so it can import the concrete node kinds (which import
// Wiring) without an import cycle.
package Wiring_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
	W "github.com/dtauraso/wirefold/nodes/Wiring"
	_ "github.com/dtauraso/wirefold/nodes/hold"
	_ "github.com/dtauraso/wirefold/nodes/input"
	_ "github.com/dtauraso/wirefold/nodes/windowandinhibitleftgate"
)

// TestGateFireAndOutputTraversal wires two Input nodes into a
// WindowAndInhibitLeftGate (FromLeft, FromRight) and the gate's ToPassed into
// a Hold node's In. left=0, right=1 → the gate NOTs left (¬0=1), ANDs with
// right (1) → fires 1. Asserts the fire happens and the resulting bead is
// delivered to the Hold node's input — end to end through the real paced
// wire — proving the gate's own goroutine (RunGate) is never parked across
// the output traversal: it must still be alive to service its per-cycle
// StepOnce loop for the delivery to complete.
func TestGateFireAndOutputTraversal(t *testing.T) {
	const topo = `{
	  "nodes": [
	    {"id":"srcL","type":"Input","data":{"init":[0],"repeat":false},
	     "outputs":[{"name":"ToPacer"}]},
	    {"id":"srcR","type":"Input","data":{"init":[1],"repeat":false},
	     "outputs":[{"name":"ToExcitatory"}]},
	    {"id":"gate","type":"WindowAndInhibitLeftGate","data":{},
	     "inputs":[{"name":"FromLeft"},{"name":"FromRight"}],
	     "outputs":[{"name":"ToPassed"}]},
	    {"id":"dst","type":"Hold","data":{"state":{"held":-1}},
	     "inputs":[{"name":"In"}]}
	  ],
	  "edges": [
	    {"label":"eL","kind":"data","source":"srcL","sourceHandle":"ToPacer","target":"gate","targetHandle":"FromLeft"},
	    {"label":"eR","kind":"data","source":"srcR","sourceHandle":"ToExcitatory","target":"gate","targetHandle":"FromRight"},
	    {"label":"eP","kind":"data","source":"gate","sourceHandle":"ToPassed","target":"dst","targetHandle":"In"}
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

	// The gate window is 3000ms and the fire dwell is 800ms, so the fire
	// itself can take up to ~3.8s of real time; then the output bead must
	// still traverse the gate->dst wire while the gate's own goroutine keeps
	// servicing its per-cycle StepOnce loop. Generous deadline for CI noise.
	deadline := time.After(10 * time.Second)
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()

	fired := false
	delivered := false
	for !fired || !delivered {
		select {
		case <-deadline:
			t.Fatalf("timed out: fired=%v delivered=%v", fired, delivered)
		case <-poll.C:
			for _, e := range live.snapshot() {
				if e.Kind == T.KindFire && e.Node == "gate" {
					fired = true
				}
				if e.Kind == T.KindRecv && e.Node == "dst" && e.Port == "In" && e.Value == 1 {
					delivered = true
				}
			}
		}
	}
}
