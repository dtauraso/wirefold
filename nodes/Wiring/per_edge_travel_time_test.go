// per_edge_travel_time_test.go — verifies per-edge travel-time on DISTINCT input ports.
//
// Two edges of different length feed one sink node through two SEPARATE input ports.
// Each edge must keep its OWN Out.SimLatencyMs (per-edge travel-time), independent of the
// other. This replaced a fan-in version (two edges into ONE port) when fan-in was removed
// from the model: an input port takes exactly one edge, so multiple sources use distinct
// ports — as every production node already does (e.g. a gate's FromLeft/FromRight).

package Wiring

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	T "github.com/dtauraso/wirefold/Trace"
)

// The two sources reuse the shared one-Out SrcNode fixture (fixture_kinds_test.go). Only
// the sink is custom here: perEdgeSink has TWO distinct paced input ports (one per feeding
// edge) — the port name is the struct field name (collectPorts, builders.go). This is the
// distinct-ports replacement for fan-in: two edges into two SEPARATE ports, never one.
type perEdgeSink struct {
	LayoutHolder
	InNear *In
	InFar  *In
}

func (n *perEdgeSink) Update(ctx context.Context) {
	<-ctx.Done()
}

func init() {
	Register("PerEdgeSink", func() any { return &perEdgeSink{} })
}

func TestPerEdgeTravelTimeDistinctPorts(t *testing.T) {
	// Two sources at different distances from the sink, each feeding its OWN sink input
	// port (srcNear->InNear, srcFar->InFar). srcNear is close (short edge); srcFar is far
	// (long edge). Positions are set via "center" messages after load (sphere-chain deliver).
	const topo = `{
	  "nodes": [
	    {"id":"srcNear","type":"SrcNode","outputs":[{"name":"Out"}]},
	    {"id":"srcFar","type":"SrcNode","outputs":[{"name":"Out"}]},
	    {"id":"sink","type":"PerEdgeSink","inputs":[{"name":"InNear"},{"name":"InFar"}]}
	  ],
	  "edges": [
	    {"label":"eNear","kind":"data","source":"srcNear","sourceHandle":"Out","target":"sink","targetHandle":"InNear"},
	    {"label":"eFar","kind":"data","source":"srcFar","sourceHandle":"Out","target":"sink","targetHandle":"InFar"}
	  ]
	}`

	dir := t.TempDir()
	path := filepath.Join(dir, "topo.json")
	if err := os.WriteFile(path, []byte(topo), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, _, nmr, _, err := LoadTopology(ctx, path, T.New(16), NewRealClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}
	nmr.Start(ctx)

	// Set positions via center messages: srcNear at (100,0,0), srcFar at (400,0,0),
	// sink at origin (no deliver needed — default center is origin).
	deliver(nmr, "srcNear", 100, 0, 0)
	deliver(nmr, "srcFar", 400, 0, 0)

	nearOut := nmr.edgeOut["eNear"]
	farOut := nmr.edgeOut["eFar"]
	if nearOut == nil || farOut == nil {
		t.Fatalf("missing per-edge Outs: near=%v far=%v", nearOut, farOut)
	}

	// 1. Distinct per-edge travel-times: the far edge is longer.
	if !(farOut.Geom().SimLatencyMs > nearOut.Geom().SimLatencyMs) {
		t.Fatalf("expected far edge slower than near: near=%v far=%v",
			nearOut.Geom().SimLatencyMs, farOut.Geom().SimLatencyMs)
	}
	if nearOut.Geom().SimLatencyMs == farOut.Geom().SimLatencyMs {
		t.Fatalf("per-edge latencies collapsed to one value: %v", nearOut.Geom().SimLatencyMs)
	}

	// 2. Node-move recomputes the moved edge's own Out latency.
	//    Move srcFar even farther; far Out latency rises.
	beforeFar := farOut.Geom().SimLatencyMs
	deliver(nmr, "srcFar", 2000, 0, 0)
	if !(farOut.Geom().SimLatencyMs > beforeFar) {
		t.Fatalf("node-move did not raise far Out latency: before=%v after=%v",
			beforeFar, farOut.Geom().SimLatencyMs)
	}
	// near edge unchanged by the move of srcFar.
	if nearOut.Geom().SimLatencyMs >= farOut.Geom().SimLatencyMs {
		t.Fatalf("near should stay below far after move: near=%v far=%v",
			nearOut.Geom().SimLatencyMs, farOut.Geom().SimLatencyMs)
	}
}

// TestFanInRejectedAtLoad pins the model boundary: two edges targeting the SAME input
// port (fan-in) must be rejected at load, not silently share a wire. Uses SinkNode's one
// "In" port with two incident edges — the exact shape validateNoFanIn forbids.
func TestFanInRejectedAtLoad(t *testing.T) {
	const topo = `{
	  "nodes": [
	    {"id":"a","type":"SrcNode","outputs":[{"name":"Out"}]},
	    {"id":"b","type":"SrcNode","outputs":[{"name":"Out"}]},
	    {"id":"sink","type":"SinkNode","inputs":[{"name":"In"}]}
	  ],
	  "edges": [
	    {"label":"eA","kind":"data","source":"a","sourceHandle":"Out","target":"sink","targetHandle":"In"},
	    {"label":"eB","kind":"data","source":"b","sourceHandle":"Out","target":"sink","targetHandle":"In"}
	  ]
	}`
	dir := t.TempDir()
	path := filepath.Join(dir, "topo.json")
	if err := os.WriteFile(path, []byte(topo), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, _, _, _, err := LoadTopology(ctx, path, T.New(16), NewRealClock()); err == nil {
		t.Fatalf("LoadTopology accepted a fan-in topology (two edges into sink.In); want a fan-in rejection error")
	}
}
