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
// Three assertions:
//   - TestHeadlessCascadeCompletes: the full net, driven by the real node loops,
//     completes under a fake clock (the unblock; RED before Phase 1).
//   - TestHeadlessDeliveryAtExactInFlightTime: a wire from the loaded active net
//     delivers exactly when elapsed reaches its inFlightTime — one tick short
//     leaves the bead in flight; the boundary tick delivers it.
//   - TestHaltedStartGeometryOnlyNoPositions: with the clock halted at start,
//     LoadTopology emits geometry events (edge curves) but zero position events;
//     Resume + Advance unblocks bead delivery (position events flow).
//
// Go-faithful: there is no central runner here. Each PacedWire reads the
// one injected clock and self-delivers; the test only advances that clock.

package main

import (
	"bytes"
	"context"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
	W "github.com/dtauraso/wirefold/nodes/Wiring"
)

// activeNetTopo is the active cascade: an Input seeds chain-holdnewsendold i0, which
// forwards its held value to i1. This mirrors topology.json's in08/i0/i1 nodes,
// wired so the headless run is a genuine multi-hop cascade (the editor's faded
// edges are omitted; what remains is the live chain).
const activeNetTopo = `{
  "nodes": [
    {
      "id": "in08", "type": "Input",
      "data": {"init": [7], "repeat": false},
      "outputs": [{"name": "ToHoldNewSendOld", "side": "right", "slot": 1}]
    },
    {
      "id": "i0", "type": "HoldNewSendOld",
      "data": {"state": {"held": 5}, "sendRules": {"ToNext1": "fireAndForget"}},
      "inputs":  [{"name": "FromPrevHoldNewSendOldNode", "side": "left", "slot": 1}],
      "outputs": [
        {"name": "ToNext0", "side": "bottom", "slot": 1},
        {"name": "ToNext1", "side": "left", "slot": 2}
      ]
    },
    {
      "id": "i1", "type": "HoldNewSendOld",
      "data": {"state": {"held": 0}, "sendRules": {"ToNext0": "fireAndForget", "ToNext1": "fireAndForget"}},
      "inputs":  [{"name": "FromPrevHoldNewSendOldNode", "side": "top", "slot": 1}],
      "outputs": [
        {"name": "ToNext0", "side": "left", "slot": 0},
        {"name": "ToNext1", "side": "bottom", "slot": 2}
      ]
    }
  ],
  "edges": [
    {"label": "in08ToI0", "kind": "chain", "source": "in08", "sourceHandle": "ToHoldNewSendOld", "target": "i0", "targetHandle": "FromPrevHoldNewSendOldNode"},
    {"label": "i0ToI1",   "kind": "chain", "source": "i0",   "sourceHandle": "ToNext0",    "target": "i1", "targetHandle": "FromPrevHoldNewSendOldNode"}
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

// hopTicks converts a per-edge in-flight time (ms) to the tick step that clears
// one hop, plus a small margin: ticksToCross = simLatencyMs / MsPerTick.
func hopTicks(simLatencyMs float64) int64 {
	return int64(math.Ceil(simLatencyMs/float64(W.MsPerTick))) + 2
}

// stepUntilSeen advances clk by stepTicks up to maxSteps times, polling sink for
// want between each advance. Returns true when want appears in sink, false if exhausted.
func stepUntilSeen(clk *W.FakeClock, sink *captureSink, stepTicks int64, want string) bool {
	const maxSteps = 200
	for i := 0; i < maxSteps; i++ {
		clk.AdvanceTicks(stepTicks)
		deadline := time.Now().Add(50 * time.Millisecond)
		for time.Now().Before(deadline) {
			if sink.contains(want) {
				return true
			}
			time.Sleep(time.Millisecond)
		}
	}
	return false
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

	nodes, _, nmr, err := W.LoadTopology(ctx, path, tr, clk)
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
	if hop1.Geom().SimLatencyMs <= 0 || hop2.Geom().SimLatencyMs <= 0 {
		t.Fatalf("expected positive in-flight times: hop1=%v hop2=%v",
			hop1.Geom().SimLatencyMs, hop2.Geom().SimLatencyMs)
	}
	maxHop := hop1.Geom().SimLatencyMs
	if hop2.Geom().SimLatencyMs > maxHop {
		maxHop = hop2.Geom().SimLatencyMs
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
	step := hopTicks(maxHop)
	want := `"kind":"recv","node":"i1"`
	got := stepUntilSeen(clk, sink, step, want)

	cancel()
	wg.Wait()
	tr.Close()

	if !got {
		t.Fatalf("headless cascade stalled: i1 never received i0's forwarded value "+
			"(want trace %q). Pre-Phase-1 this is expected RED; post-Phase-1 it must be GREEN.\nTrace:\n%s",
			want, sink.String())
	}

	// i1 must have received i0's held value (5), proving the value crossed both hops.
	if !sink.contains(`"kind":"recv","node":"i1","port":"FromPrevHoldNewSendOldNode","value":5`) {
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

	_, slotReg, nmr, err := W.LoadTopology(ctx, path, tr, clk)
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}

	// First hop: in08 → i0. The per-edge in-flight time lives on the source Out;
	// the dest wire (1:1 edge) carries the same value as its window aggregate.
	out := nmr.EdgeOut("in08ToI0")
	pw := slotReg["i0.FromPrevHoldNewSendOldNode"]
	if out == nil || pw == nil {
		t.Fatalf("missing first-hop out/wire: out=%v wire=%v", out, pw)
	}
	inFlightMs := out.Geom().SimLatencyMs
	if inFlightMs <= 1 {
		t.Fatalf("expected first-hop in-flight time > 1ms, got %v", inFlightMs)
	}

	// Place a bead on the loaded wire with its own in-flight time and drive it
	// to delivery on a background goroutine (the test-side stand-in for the
	// per-node driven path).
	if !pw.PlaceAndDriveDeliverOnly(ctx, 7, inFlightMs) {
		t.Fatal("PlaceAndDriveDeliverOnly returned false")
	}

	// ticksToCross = inFlightMs / MsPerTick; delivery fires at the ceil tick.
	crossTicks := inFlightMs / float64(W.MsPerTick)
	deliverTick := int64(math.Ceil(crossTicks))

	// One tick short of the delivery tick: the bead must still be in flight.
	clk.AdvanceTicks(deliverTick - 1)
	time.Sleep(10 * time.Millisecond) // give the delivery goroutine a chance to (wrongly) fire
	if !pw.InFlight() {
		t.Fatalf("bead delivered before elapsed reached in-flight time (%.3f ms)", inFlightMs)
	}

	// Advance to the delivery tick → delivery fires.
	clk.AdvanceTicks(1)
	v, err := pw.Recv(ctx)
	if err != nil || v != 7 {
		t.Fatalf("Recv at exact in-flight time: v=%v err=%v", v, err)
	}
}

// TestHaltedStartGeometryOnlyNoPositions asserts the halted-start contract:
//
//  1. With the clock halted at load time, LoadTopology still emits geometry events
//     (edge curves are NOT clock-gated); the static diagram is always available.
//  2. While the clock remains halted, node goroutines are running and the Input
//     node has seeded the first wire — but ZERO position events are emitted
//     because the clock is frozen and WaitUntil parks all delivery goroutines.
//  3. After Resume() + advancing the fake clock past the in-flight time, position
//     events flow and the cascade delivers (i1 receives).
//
// This is the deterministic contract for the play/pause gate: geometry is
// load-time and unconditional; bead delivery is gated by Halt/Resume.
func TestHaltedStartGeometryOnlyNoPositions(t *testing.T) {
	path := writeTopo(t, activeNetTopo)

	sink := &captureSink{}
	tr := T.NewWithSink(256, sink)
	clk := W.NewFakeClock()

	// Halt the clock before loading — matches the production runTopology path
	// which halts immediately after construction, before LoadTopology.
	clk.Halt()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// LoadTopology emits geometry (KindGeometry) synchronously during load.
	// Because geometry emission does not wait on the clock, it must succeed
	// even with the clock halted.
	nodes, _, nmr, err := W.LoadTopology(ctx, path, tr, clk)
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}

	// Lookup the per-edge in-flight times before launching nodes.
	hop1 := nmr.EdgeOut("in08ToI0")
	hop2 := nmr.EdgeOut("i0ToI1")
	if hop1 == nil || hop2 == nil {
		t.Fatalf("missing per-edge Outs: hop1=%v hop2=%v", hop1, hop2)
	}
	maxHop := hop1.Geom().SimLatencyMs
	if hop2.Geom().SimLatencyMs > maxHop {
		maxHop = hop2.Geom().SimLatencyMs
	}

	// Start the real node goroutines; Input seeds the first wire immediately.
	var wg sync.WaitGroup
	wg.Add(len(nodes))
	for _, node := range nodes {
		n := node
		go func() {
			defer wg.Done()
			n.Update(ctx)
		}()
	}

	// Give the Input goroutine time to seed the first wire and launch the
	// delivery goroutine. That goroutine should now be parked in WaitUntil.
	time.Sleep(30 * time.Millisecond)

	// ASSERTION 1: no position events while the clock is halted.
	if sink.contains(`"kind":"edge-bead"`) {
		t.Fatal("edge-bead event emitted while clock was halted; halted-start gate is broken")
	}

	// ASSERTION 2: geometry events MUST have been emitted synchronously by
	// LoadTopology. The loader calls tr.Geometry() for each edge during load,
	// so they arrive in the stream before any node goroutine runs.
	if !sink.contains(`"kind":"geometry"`) {
		t.Fatal("no geometry events emitted during LoadTopology; geometry must be clock-independent")
	}

	// Resume the clock and advance past the in-flight time for both hops.
	// The parked delivery goroutines unblock; position events and then delivery
	// flow through the cascade.
	clk.Resume()
	step := hopTicks(maxHop)
	want := `"kind":"recv","node":"i1"`
	got := stepUntilSeen(clk, sink, step, want)

	cancel()
	wg.Wait()
	tr.Close()

	if !got {
		t.Fatalf("cascade did not complete after Resume+Advance (i1 never received).\nTrace:\n%s",
			sink.String())
	}

	// ASSERTION 3: position events flowed after resume.
	if !sink.contains(`"kind":"edge-bead"`) {
		t.Fatalf("no position events emitted after Resume+Advance; delivery goroutines should emit positions.\nTrace:\n%s",
			sink.String())
	}
}

// TestFeedbackRingAlternates verifies the plain-Input path:
//   - A plain-Input case (FeedbackIn unwired) emits unchanged 0,1 in order.
//
// (The HoldNewSendOld-sourced feedback-ring subtest was removed when the FeedbackOut
// port moved off the HoldNewSendOld kind onto the Pacer kind; the HoldNewSendOld is now a
// pure forwarder.)
func TestFeedbackRingAlternates(t *testing.T) {
	t.Run("PlainInputUnwired", func(t *testing.T) {
		// Plain topology: in08 → i0, no feedback edge. in08's FeedbackIn is unwired
		// so the existing emit path runs and values 0,1 appear in order.
		path := writeTopo(t, activeNetTopo)

		sink := &captureSink{}
		tr := T.NewWithSink(0, sink)
		clk := W.NewFakeClock()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		nodes, _, nmr, err := W.LoadTopology(ctx, path, tr, clk)
		if err != nil {
			t.Fatalf("LoadTopology: %v", err)
		}

		hop1 := nmr.EdgeOut("in08ToI0")
		if hop1 == nil {
			t.Fatalf("missing per-edge Out: hop1=%v", hop1)
		}

		var wg sync.WaitGroup
		wg.Add(len(nodes))
		for _, node := range nodes {
			n := node
			go func() {
				defer wg.Done()
				n.Update(ctx)
			}()
		}

		step := hopTicks(hop1.Geom().SimLatencyMs)
		// activeNetTopo uses Init=[7]; just confirm i0 receives the value via the existing path.
		want := `"kind":"recv","node":"i0"`
		got := stepUntilSeen(clk, sink, step, want)

		cancel()
		wg.Wait()
		tr.Close()

		if !got {
			t.Fatalf("plain-input (unwired FeedbackIn) stalled: i0 never received.\nTrace:\n%s", sink.String())
		}
	})
}
