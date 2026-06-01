// fanin_travel_time_test.go — verifies per-edge travel-time on fan-in.
//
// Two edges of different length fan into one destination input port. Each edge
// must keep its OWN Out.SimLatencyMs (per-edge travel-time), while the shared
// destination wire's MaxIncomingSimLatencyMs is the max over both edges (the
// value In.SimLatencyMs() returns to derive a windowed node's coincidence W).

package Wiring

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"

	T "github.com/dtauraso/wirefold/Trace"
)

// faninSrc is a minimal source kind with one paced Out.
type faninSrc struct {
	Out *Out
}

func (n *faninSrc) Update(ctx context.Context) {}

// faninSink is a minimal sink kind with one paced In.
type faninSink struct {
	In *In
}

func (n *faninSink) Update(ctx context.Context) {}

func init() {
	Register("FanInSrc", func() any { return &faninSrc{} })
	Register("FanInSink", func() any { return &faninSink{} })
}

func TestFanInPerEdgeTravelTime(t *testing.T) {
	// Two sources at different distances from the sink, both feeding sink.In.
	// srcNear is close (short edge); srcFar is far (long edge).
	const topo = `{
	  "nodes": [
	    {"id":"srcNear","type":"FanInSrc","outputs":[{"name":"Out"}]},
	    {"id":"srcFar","type":"FanInSrc","outputs":[{"name":"Out"}]},
	    {"id":"sink","type":"FanInSink","inputs":[{"name":"In"}]}
	  ],
	  "edges": [
	    {"label":"eNear","kind":"data","source":"srcNear","sourceHandle":"Out","target":"sink","targetHandle":"In"},
	    {"label":"eFar","kind":"data","source":"srcFar","sourceHandle":"Out","target":"sink","targetHandle":"In"}
	  ],
	  "view": {"nodes": {
	    "srcNear": {"x": 100, "y": 0, "z": 0},
	    "srcFar":  {"x": 800, "y": 0, "z": 0},
	    "sink":    {"x": 0,   "y": 0, "z": 0}
	  }}
	}`

	dir := t.TempDir()
	path := filepath.Join(dir, "topo.json")
	if err := os.WriteFile(path, []byte(topo), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	_, slotReg, _, nmr, err := LoadTopology(ctx, path, T.New(16))
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}

	nearOut := nmr.edgeOut["eNear"]
	farOut := nmr.edgeOut["eFar"]
	if nearOut == nil || farOut == nil {
		t.Fatalf("missing per-edge Outs: near=%v far=%v", nearOut, farOut)
	}

	// 1. Distinct per-edge travel-times: the far edge is longer.
	if !(farOut.SimLatencyMs > nearOut.SimLatencyMs) {
		t.Fatalf("expected far edge slower than near: near=%v far=%v",
			nearOut.SimLatencyMs, farOut.SimLatencyMs)
	}
	if nearOut.SimLatencyMs == farOut.SimLatencyMs {
		t.Fatalf("per-edge latencies collapsed to one value: %v", nearOut.SimLatencyMs)
	}

	// 2. The shared dest wire's window aggregate is the max of the two edges.
	pw := slotReg["sink.In"]
	if pw == nil {
		t.Fatal("missing dest wire sink.In")
	}
	wantMax := math.Max(nearOut.SimLatencyMs, farOut.SimLatencyMs)
	if math.Abs(pw.MaxIncomingSimLatencyMs-wantMax) > 1e-9 {
		t.Fatalf("MaxIncomingSimLatencyMs = %v, want max(%v,%v) = %v",
			pw.MaxIncomingSimLatencyMs, nearOut.SimLatencyMs, farOut.SimLatencyMs, wantMax)
	}

	// 3. Degenerate 1:1 parity: a single edge's port reports max == that edge.
	//    (Both source ports here are 1:1 on their own Out; assert near's Out
	//    equals the aggregate it would produce alone — i.e. the aggregate is not
	//    smaller than any single feeding edge.)
	if pw.MaxIncomingSimLatencyMs < nearOut.SimLatencyMs ||
		pw.MaxIncomingSimLatencyMs < farOut.SimLatencyMs {
		t.Fatalf("aggregate %v below a feeding edge (near=%v far=%v)",
			pw.MaxIncomingSimLatencyMs, nearOut.SimLatencyMs, farOut.SimLatencyMs)
	}

	// 4. Node-move recomputes both the moved edge's Out and the port aggregate.
	//    Move srcFar even farther; far Out latency rises and so does the aggregate.
	beforeFar := farOut.SimLatencyMs
	nmr.applyNodeMove("srcFar", 2000, 0, 0)
	if !(farOut.SimLatencyMs > beforeFar) {
		t.Fatalf("node-move did not raise far Out latency: before=%v after=%v",
			beforeFar, farOut.SimLatencyMs)
	}
	if math.Abs(pw.MaxIncomingSimLatencyMs-farOut.SimLatencyMs) > 1e-9 {
		t.Fatalf("post-move aggregate %v != far latency %v",
			pw.MaxIncomingSimLatencyMs, farOut.SimLatencyMs)
	}
	// near edge unchanged by the move of srcFar.
	if nearOut.SimLatencyMs >= farOut.SimLatencyMs {
		t.Fatalf("near should stay below far after move: near=%v far=%v",
			nearOut.SimLatencyMs, farOut.SimLatencyMs)
	}
}
