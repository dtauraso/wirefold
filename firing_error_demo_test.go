// firing_error_demo_test.go — end-to-end firing-error emission (main package).
//
// Proves the processing-window error path fires end-to-end through the REAL node
// loops and the REAL trace stream, deterministically under a fake clock. The
// scenario is the same one the topology-firing-demo/ tree stages for the editor:
//
//	src (Input, alternating 0/1, repeat) ──short──▶ h (HoldNewSendOld) ──LONG──▶ snk
//
// The output wire h→snk is made much longer than the input wire src→h, so h's
// processing window (which spans until its output bead finishes transit) is wide
// enough that src's NEXT, DIFFERENT-color bead lands on h's input port mid-window.
// The shared ProcessingGuard then emits a node-status torusRed=true carrying the
// missed value, discards the bead, and emits a torusRed=false revert when the
// window completes. This is the live-editor "watch the torus flip red" moment,
// asserted here without an editor attached.
//
// It lives in package main because that is the only package importing every node
// kind (kinds_generated.go), so LoadTopology can construct the real src/h/snk loops.
//
// Determinism: the Input self-paces by blocking on each src→h transit, so beads
// enter h one input-latency apart while h's window stays open for a full (much
// larger) output latency. No wall-clock timing — the fake clock is advanced in
// bounded steps and the trace stream is polled between advances.

package main

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

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
      "id": "src", "type": "Input", "r": 8, "x": 0, "y": 0, "z": 0,
      "data": {"init": [0, 1], "repeat": true},
      "outputs": [{"name": "ToHoldNewSendOld"}]
    },
    {
      "id": "h", "type": "HoldNewSendOld", "r": 8, "x": 60, "y": 0, "z": 0,
      "data": {"state": {"held": -1}, "sendRules": {"ToNext1": "fireAndForget"}},
      "inputs":  [{"name": "FromPrevHoldNewSendOldNode"}],
      "outputs": [{"name": "ToNext0"}, {"name": "ToNext1"}]
    },
    {
      "id": "snk", "type": "HoldNewSendOld", "r": 8, "x": 1060, "y": 0, "z": 0,
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

// TestFiringErrorEmittedEndToEnd drives the demo scenario through the real node
// loops and asserts the node-status error/revert pair Go streams for it.
func TestFiringErrorEmittedEndToEnd(t *testing.T) {
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

	// Advance the fake clock in input-latency-sized steps, polling for the red
	// event first, then the revert. The step clears an input hop each time so the
	// Input keeps feeding beads into h's open window.
	step := time.Duration(in.Geom().SimLatencyMs*float64(time.Millisecond)) + time.Millisecond

	// The FIRST real window is opened by value 0 (lastVal=0), so the missed
	// different-color bead is value 1 — deterministic given init [0,1].
	wantRed := `"node":"h","torusRed":true,"missedValue":1`
	if !stepUntilSeen(clk, sink, step, wantRed) {
		cancel()
		wg.Wait()
		tr.Close()
		t.Fatalf("firing error never emitted (want %q).\nTrace:\n%s", wantRed, sink.String())
	}

	// Drive the window to completion so the revert fires. Keep stepping until a
	// torusRed=false node-status for h appears.
	wantRevert := `"node":"h","torusRed":false`
	got := stepUntilSeen(clk, sink, step, wantRevert)

	cancel()
	wg.Wait()
	tr.Close()

	if !got {
		t.Fatalf("firing error red never reverted (want %q).\nTrace:\n%s", wantRevert, sink.String())
	}

	// The red must precede its revert in the stream (error entered, then cleared).
	full := sink.String()
	if strings.Index(full, wantRed) > strings.Index(full, wantRevert) {
		t.Fatalf("revert appeared before the red event; expected red then revert.\nTrace:\n%s", full)
	}
}
