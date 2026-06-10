// headless_cascade_test.go — Phase 1 headless verifier (main package).
//
// Proves the Go network runs to completion with NO editor attached: a fake clock
// drives wire delivery, the real node goroutines run, and the cascade
// in08 → i0 → i1 completes (i1 receives i0's forwarded value) within bounded
// fake-time. Pre-Phase-1 this STALLS — delivery was triggered only by a TS
// "delivered" stdin message, so with no editor the first hop never delivers and
// i1 never receives. After Phase 1, Go's clock self-delivers and the cascade
// completes.
//
// It lives in package main because that is the only package that imports every
// node kind (via kinds_generated.go), so the loader can construct the real
// in08/i0/i1 node loops.
//
// Two assertions:
//   - TestHeadlessCascadeCompletes: the full net, driven by the real node loops,
//     completes under a fake clock (the unblock; RED before Phase 1).
//   - TestHeadlessDeliveryAtExactInFlightTime: a wire from the loaded active net
//     delivers exactly when elapsed reaches its inFlightTime — one tick short
//     leaves the bead in flight; the boundary tick delivers it.
//
// Substrate-faithful: there is no central runner here. Each PacedWire reads the
// one injected clock and self-delivers; the test only advances that clock.

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
	W "github.com/dtauraso/wirefold/nodes/Wiring"
)

// activeNetTopo is the active cascade: an Input seeds chain-inhibitor i0, which
// forwards its held value to i1. This mirrors topology.json's in08/i0/i1 nodes,
// wired so the headless run is a genuine multi-hop cascade (the editor's faded
// edges are omitted; what remains is the live chain).
const activeNetTopo = `{
  "nodes": [
    {
      "id": "in08", "type": "Input",
      "data": {"init": [7], "repeat": false},
      "outputs": [{"name": "ToReadGate", "side": "right", "slot": 1}]
    },
    {
      "id": "i0", "type": "ChainInhibitor",
      "data": {"state": {"held": 5}, "sendRules": {"ToNext1": "fireAndForget"}},
      "inputs":  [{"name": "FromPrevChainInhibitorNode", "side": "left", "slot": 1}],
      "outputs": [
        {"name": "ToNext0", "side": "bottom", "slot": 1},
        {"name": "ToNext1", "side": "left", "slot": 2}
      ]
    },
    {
      "id": "i1", "type": "ChainInhibitor",
      "data": {"state": {"held": 0}, "sendRules": {"ToNext0": "fireAndForget", "ToNext1": "fireAndForget"}},
      "inputs":  [{"name": "FromPrevChainInhibitorNode", "side": "top", "slot": 1}],
      "outputs": [
        {"name": "ToNext0", "side": "left", "slot": 0},
        {"name": "ToNext1", "side": "bottom", "slot": 2}
      ]
    }
  ],
  "edges": [
    {"label": "in08ToI0", "kind": "chain", "source": "in08", "sourceHandle": "ToReadGate", "target": "i0", "targetHandle": "FromPrevChainInhibitorNode"},
    {"label": "i0ToI1",   "kind": "chain", "source": "i0",   "sourceHandle": "ToNext0",    "target": "i1", "targetHandle": "FromPrevChainInhibitorNode"}
  ],
  "view": {"nodes": {
    "in08": {"x": 19,  "y": 314},
    "i0":   {"x": 281, "y": 266},
    "i1":   {"x": 307, "y": 330}
  }}
}`

// captureSink is a concurrent-safe io.Writer that accumulates the trace JSONL
// stream so the test can poll for the i1 recv event while the net runs.
type captureSink struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *captureSink) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *captureSink) contains(sub string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return bytes.Contains(c.buf.Bytes(), []byte(sub))
}

func (c *captureSink) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

func writeTopo(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "topo.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write topo: %v", err)
	}
	return path
}

// TestHeadlessCascadeCompletes drives the real node loops with a fake clock and
// asserts i1 receives i0's forwarded value (held=5) within a bounded fake-time
// budget. RED before Phase 1 (no clock delivery → first hop stalls → i1 never
// receives → budget exhausted).
func TestHeadlessCascadeCompletes(t *testing.T) {
	path := writeTopo(t, activeNetTopo)

	sink := &captureSink{}
	tr := T.NewWithSink(0, sink)
	clk := W.NewFakeClock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nodes, _, _, nmr, err := W.LoadTopology(ctx, path, tr, clk)
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}

	// Per-edge in-flight times from the loaded geometry (the values Send passes
	// to the clock). The fake-time step must clear both hops.
	hop1 := nmr.EdgeOut("in08ToI0")
	hop2 := nmr.EdgeOut("i0ToI1")
	if hop1 == nil || hop2 == nil {
		t.Fatalf("missing per-edge Outs: hop1=%v hop2=%v", hop1, hop2)
	}
	if hop1.SimLatencyMs <= 0 || hop2.SimLatencyMs <= 0 {
		t.Fatalf("expected positive in-flight times: hop1=%v hop2=%v",
			hop1.SimLatencyMs, hop2.SimLatencyMs)
	}
	maxHop := hop1.SimLatencyMs
	if hop2.SimLatencyMs > maxHop {
		maxHop = hop2.SimLatencyMs
	}

	// Run the real node goroutines headlessly.
	var wg sync.WaitGroup
	wg.Add(len(nodes))
	for _, node := range nodes {
		n := node
		go func() {
			defer wg.Done()
			n.Update(ctx)
		}()
	}

	// Advance the fake clock in steps that clear a hop each time, polling for the
	// i1 recv. The cascade is two hops, so a handful of steps suffices; the budget
	// is generous to absorb goroutine scheduling between placement and delivery.
	step := time.Duration(maxHop*float64(time.Millisecond)) + time.Millisecond
	const maxSteps = 200
	want := `"kind":"recv","node":"i1"`
	got := false
	for i := 0; i < maxSteps; i++ {
		clk.Advance(step)
		// Let the woken delivery goroutines and node loops make progress before
		// the next advance.
		deadline := time.Now().Add(50 * time.Millisecond)
		for time.Now().Before(deadline) {
			if sink.contains(want) {
				got = true
				break
			}
			time.Sleep(time.Millisecond)
		}
		if got {
			break
		}
	}

	cancel()
	wg.Wait()
	tr.Close()

	if !got {
		t.Fatalf("headless cascade stalled: i1 never received i0's forwarded value "+
			"(want trace %q). Pre-Phase-1 this is expected RED; post-Phase-1 it must be GREEN.\nTrace:\n%s",
			want, sink.String())
	}

	// i1 must have received i0's held value (5), proving the value crossed both hops.
	if !sink.contains(`"kind":"recv","node":"i1","port":"FromPrevChainInhibitorNode","value":5`) {
		t.Fatalf("i1 received, but not i0's forwarded held value 5.\nTrace:\n%s", sink.String())
	}
}

// TestHeadlessDeliveryAtExactInFlightTime loads the active net and asserts that a
// wire delivers exactly when elapsed reaches its inFlightTime: one tick short of
// the in-flight time leaves the bead in flight; the boundary tick delivers it.
// Uses the first hop's real loaded geometry (in08 → i0) so the asserted in-flight
// time is the topology's own value, not a synthetic one.
func TestHeadlessDeliveryAtExactInFlightTime(t *testing.T) {
	path := writeTopo(t, activeNetTopo)

	tr := T.New(0)
	defer tr.Close()
	clk := W.NewFakeClock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, slotReg, _, nmr, err := W.LoadTopology(ctx, path, tr, clk)
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}

	// First hop: in08 → i0. The per-edge in-flight time lives on the source Out;
	// the dest wire (1:1 edge) carries the same value as its window aggregate.
	out := nmr.EdgeOut("in08ToI0")
	pw := slotReg["i0.FromPrevChainInhibitorNode"]
	if out == nil || pw == nil {
		t.Fatalf("missing first-hop out/wire: out=%v wire=%v", out, pw)
	}
	inFlightMs := out.SimLatencyMs
	if inFlightMs <= 1 {
		t.Fatalf("expected first-hop in-flight time > 1ms, got %v", inFlightMs)
	}

	// Place a bead on the loaded wire with its own in-flight time.
	if err := pw.SendDeliverOnly(ctx, 7, inFlightMs); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// One whole millisecond short of the deadline: the bead must still be in flight.
	shortOf := time.Duration((inFlightMs-1)*float64(time.Millisecond)) - 1
	clk.Advance(shortOf)
	time.Sleep(10 * time.Millisecond) // give the delivery goroutine a chance to (wrongly) fire
	if !pw.InFlight() {
		t.Fatalf("bead delivered before elapsed reached in-flight time (%.3f ms)", inFlightMs)
	}

	// Advance to elapsed == inFlightTime exactly → delivery fires.
	clk.Advance(time.Duration(inFlightMs*float64(time.Millisecond)) - shortOf)
	v, err := pw.Recv(ctx)
	if err != nil || v != 7 {
		t.Fatalf("Recv at exact in-flight time: v=%v err=%v", v, err)
	}
	pw.Done()
}
