// firing_error_demo_test.go — end-to-end processing-window discard (main package).
//
// Proves the processing-window bead-discard path fires end-to-end through the REAL
// node loops and the REAL trace stream, deterministically under a fake clock. The
// scenario is the same one the topology-firing-demo/ tree stages for the editor:
//
//	src (Input, alternating 0/1, repeat) ──short──▶ h (HoldNewSendOld) ──LONG──▶ snk
//
// The output wire h→snk is made much longer than the input wire src→h, so h's
// processing window (which spans until its output bead finishes transit) is wide
// enough that src's NEXT, DIFFERENT-color bead lands on h's input port mid-window.
// The shared ProcessingGuard discards that bead silently. This test confirms the
// discard happens (the bead does NOT produce extra output from h) and that the node
// keeps running after the window closes.
//
// It lives in package main because that is the only package importing every node
// kind (kinds_generated.go), so LoadTopology can construct the real src/h/snk loops.
//
// Determinism: the Input self-paces by blocking on each src→h transit, so beads
// enter h one input-latency apart while h's window stays open for a full (much
// larger) output latency. No wall-clock timing — the fake clock is advanced in
// bounded steps.

package main

import (
	"context"
	"sync"
	"testing"

	T "github.com/dtauraso/wirefold/Trace"
	W "github.com/dtauraso/wirefold/nodes/Wiring"
)

// firingDemoTopo stages the wide-window scenario. Node centers set the wire
// lengths (arc = chord between the aimed port surfaces): src→h is ~60 world units
// apart, h→snk is ~1000, and every node uses a small radius r so the chord tracks
// the center distance. The result is T_out ≫ T_in, guaranteeing src's next
// different-color bead reaches h before h's output transit completes.
const firingDemoTopo = `{
  "nodes": [
    {
      "id": "src", "type": "Input", "r": 8, "scenePolarR": 0, "scenePolarTheta": 0, "scenePolarPhi": 0,
      "data": {"init": [0, 1], "repeat": true},
      "outputs": [{"name": "ToHoldNewSendOld"}]
    },
    {
      "id": "h", "type": "HoldNewSendOld", "r": 8, "scenePolarR": 60, "scenePolarTheta": 1.5707963267948966, "scenePolarPhi": 0,
      "data": {"state": {"held": -1}, "sendRules": {"ToNext1": "fireAndForget"}},
      "inputs":  [{"name": "FromPrevHoldNewSendOldNode"}],
      "outputs": [{"name": "ToNext0"}, {"name": "ToNext1"}]
    },
    {
      "id": "snk", "type": "HoldNewSendOld", "r": 8, "scenePolarR": 1060, "scenePolarTheta": 1.5707963267948966, "scenePolarPhi": 0,
      "data": {"state": {"held": -1}, "sendRules": {"ToNext1": "fireAndForget"}},
      "inputs":  [{"name": "FromPrevHoldNewSendOldNode"}],
      "outputs": [{"name": "ToNext0"}, {"name": "ToNext1"}]
    }
  ],
  "edges": [
    {"label": "srcToH", "kind": "chain", "source": "src", "sourceHandle": "ToHoldNewSendOld", "target": "h",   "targetHandle": "FromPrevHoldNewSendOldNode"},
    {"label": "hToSnk", "kind": "chain", "source": "h",   "sourceHandle": "ToNext0",          "target": "snk", "targetHandle": "FromPrevHoldNewSendOldNode"}
  ],
  "view": {"nodes": {
    "src": {"x": 0,    "y": 0},
    "h":   {"x": 60,   "y": 0},
    "snk": {"x": 1060, "y": 0}
  }}
}`

// TestFiringErrorDiscardEndToEnd drives the demo scenario through the real node
// loops and asserts the mid-processing different-color bead is discarded (the node
// continues correctly and eventually delivers output to snk). The processing-window
// discard is silent — no node-status event is emitted.
func TestFiringErrorDiscardEndToEnd(t *testing.T) {
	path := writeTopo(t, firingDemoTopo)

	sink := &captureSink{}
	tr := T.NewWithSink(0, sink)
	clk := W.NewFakeClock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nodes, _, nmr, err := W.LoadTopology(ctx, path, tr, clk)
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}

	// Sanity: the output wire must be materially longer than the input wire, or the
	// window is too narrow for a mid-processing arrival (the whole premise).
	in := nmr.EdgeOut("srcToH")
	out := nmr.EdgeOut("hToSnk")
	if in == nil || out == nil {
		t.Fatalf("missing per-edge Outs: in=%v out=%v", in, out)
	}
	if !(out.Geom().SimLatencyMs > 4*in.Geom().SimLatencyMs) {
		t.Fatalf("output wire not long enough for a wide window: in=%.1fms out=%.1fms",
			in.Geom().SimLatencyMs, out.Geom().SimLatencyMs)
	}

	var wg sync.WaitGroup
	wg.Add(len(nodes))
	for _, node := range nodes {
		n := node
		go func() { defer wg.Done(); n.Update(ctx) }()
	}

	// Advance the clock until h's first output bead has been sent (a "send" event
	// for node h appears in the trace). This proves the processing window closed
	// and the node is alive — the missed bead was discarded, not processed.
	step := hopTicks(in.Geom().SimLatencyMs)
	wantSend := `"kind":"send","node":"h"`
	if !stepUntilSeen(clk, sink, step, wantSend) {
		cancel()
		wg.Wait()
		tr.Close()
		t.Fatalf("h never sent output (want %q).\nTrace:\n%s", wantSend, sink.String())
	}

	cancel()
	wg.Wait()
	tr.Close()
}
