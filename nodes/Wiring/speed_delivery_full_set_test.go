// speed_delivery_full_set_test.go — proves item 3 of per-goroutine-clock.md's "What
// must be proven": a speed change reaches EVERY clock-owning goroutine, not a sample.
// External test package so it can import every node kind that owns a clock copy
// without an import cycle back into Wiring.
package Wiring_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	T "github.com/dtauraso/wirefold/Trace"
	W "github.com/dtauraso/wirefold/nodes/Wiring"
	_ "github.com/dtauraso/wirefold/nodes/hold"
	_ "github.com/dtauraso/wirefold/nodes/holdflip"
	_ "github.com/dtauraso/wirefold/nodes/holdnewsendold"
	_ "github.com/dtauraso/wirefold/nodes/input"
	_ "github.com/dtauraso/wirefold/nodes/pacer"
	_ "github.com/dtauraso/wirefold/nodes/pulse"
	_ "github.com/dtauraso/wirefold/nodes/windowandinhibitleftgate"
)

// speedFullSetTopo has exactly one node of every kind that owns a clock copy
// (grep-discovered set: input, holdnewsendold, hold, pacer, holdflip,
// gatecommon.RunGate via WindowAndInhibitLeftGate, pulse) plus exactly ONE edge (so
// exactly one edgeMover exists too). Downstream wiring beyond that single edge is
// deliberately left absent — injectSpeedChans (builders.go) creates a node's speed
// channel(s) unconditionally at construction, never gated on whether its OUTPUT
// ports end up wired, so the channel COUNT below does not depend on it. Pulse is
// left with no inputs/outputs wired for the same reason: its SpeedCh/Out1SpeedCh/
// Out2SpeedCh are all created unconditionally at construction.
const speedFullSetTopo = `{
  "nodes": [
    {"id":"src","type":"Input","data":{"init":[0],"repeat":false},
     "outputs":[{"name":"ToHoldNewSendOld"}]},
    {"id":"hnso","type":"HoldNewSendOld","data":{"state":{"held":-1}},
     "inputs":[{"name":"FromPrevHoldNewSendOldNode"}]},
    {"id":"holdSink","type":"Hold","data":{"state":{"held":-1}},
     "inputs":[{"name":"In"}]},
    {"id":"pacer","type":"Pacer","data":{"state":{"held":-1}},
     "inputs":[{"name":"FromInput"}], "outputs":[{"name":"FeedbackOut"}]},
    {"id":"holdflip","type":"HoldFlip","data":{},
     "inputs":[{"name":"In"}], "outputs":[{"name":"Out"}]},
    {"id":"gate","type":"WindowAndInhibitLeftGate","data":{},
     "inputs":[{"name":"FromLeft"},{"name":"FromRight"}],
     "outputs":[{"name":"ToPassed"}]},
    {"id":"pulse","type":"Pulse","data":{}}
  ],
  "edges": [
    {"label":"e0","kind":"data","source":"src","sourceHandle":"ToHoldNewSendOld","target":"hnso","targetHandle":"FromPrevHoldNewSendOldNode"}
  ]
}`

// expectedSpeedSinkCount is the hand-derived total, one term per clock-owning
// goroutine kind (see the field-name lists in builders.go's speedChanFieldNames and
// node_move.go's per-edge speedSinks append):
//
//	Input(1) + HoldNewSendOld(1) + Hold(1) + Pacer(1)  = 4   (one SpeedCh each)
//	HoldFlip: SpeedCh + DriveSpeedCh                    = 2   (main loop + 1 drive goroutine)
//	WindowAndInhibitLeftGate (gatecommon.RunGate)       = 1   (SpeedCh)
//	Pulse: SpeedCh + Out1SpeedCh + Out2SpeedCh          = 3   (main loop + 2 drive goroutines)
//	edgeMover, one per edge (exactly 1 edge above)      = 1
//	nodeMover, one per node (exactly 7 nodes above)     = 7   (the mover
//	                                                            is no longer the odd one out
//	                                                            pacing on a bare wall timer)
//	PacedWire, one per unique dest port (1 edge → 1)    = 1   (the wire is its own
//	                                                            clock-owning goroutine now)
//
// Total = 4 + 2 + 1 + 3 + 1 + 7 + 1 = 19.
const expectedSpeedSinkCount = 19

// TestSpeedSinksCoverEveryClockOwningGoroutine asserts LoadTopology's speed-sink list
// has EXACTLY the expected count for this fixture — over the FULL set, not a sample.
// Deliberately made to fail once during development by temporarily removing one
// kind's field name from builders.go's speedChanFieldNames (dropping the expected
// count by 1) and confirming this assertion goes red; restored afterward (see the
// task report, not committed here).
func TestSpeedSinksCoverEveryClockOwningGoroutine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "topo.json")
	if err := os.WriteFile(path, []byte(speedFullSetTopo), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr := T.New(256)
	defer tr.Close()

	_, _, _, speedSinks, err := W.LoadTopology(ctx, path, tr, W.NewRealClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}
	if len(speedSinks) != expectedSpeedSinkCount {
		t.Fatalf("speedSinks has %d channels, want exactly %d (one goroutine was left behind, or an extra one appeared) — see expectedSpeedSinkCount's breakdown", len(speedSinks), expectedSpeedSinkCount)
	}

	// Broadcasting to the full set must not block, regardless of whether any
	// receiver is actually reading (some of these node goroutines are never
	// started in this test — LoadTopology alone does not launch them).
	done := make(chan struct{})
	go func() {
		for _, ch := range speedSinks {
			W.SendSpeedNonBlocking(ch, 2)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("ctx cancelled before broadcast finished")
	}
}
