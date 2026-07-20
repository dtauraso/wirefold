// unfed_port_test.go — an unfed required port must LOAD and stay INERT, not panic.
//
// validate.go deliberately has no required-inbound-edge check: "a node with an unfed
// required port loads and stays inert by precondition-gating". That made an unwired In an
// ordinary, reachable topology state — and every pacing loop here does
// `clk := h.In.Clock()` then `clk.SleepCycle(ctx)` with no guard, so when In.Clock()
// returned nil for an unwired port the node dereferenced a nil interface. Node goroutines
// run with no recover() (main.go), so that one unfed port took down every other node and
// the buffer stream with it.
//
// This test drives the REAL loader on a REAL spec file — the runtime's own input form —
// because the panic lived in the wiring path, not in Node's logic: every other test in
// this package wires the input port, which is exactly why the crash went unnoticed.
//
// Per-goroutine-clock.md's API demolition (docs/planning/visual-editor/
// per-goroutine-clock.md) removed In.Clock()/Out.Clock() entirely — a node now
// carries its OWN Clock field (seeded by reflectBuild from the loader's origin) and
// Copies it once at its own goroutine's start, independent of whether any port is
// wired. The nil-clock hazard this test guards moved from the port to that field;
// see the Clock assertion below.
package hold

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
	"github.com/dtauraso/wirefold/nodes/Wiring"
)

func TestUnfedRequiredPortLoadsAndStaysInert(t *testing.T) {
	// A Hold with its required In port declared but NO edge feeding it. validateSpec
	// accepts this by design; the editor no longer flags it either, so it is silent.
	const topo = `{
	  "nodes": [{"id":"h","type":"Hold","data":{"state":{"held":-1}},"inputs":[{"name":"In"}]}],
	  "edges": []
	}`

	dir := t.TempDir()
	path := filepath.Join(dir, "topo.json")
	if err := os.WriteFile(path, []byte(topo), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nodes, _, _, _, err := Wiring.LoadTopology(ctx, path, T.New(16), Wiring.NewRealClock())
	if err != nil {
		t.Fatalf("LoadTopology rejected an unfed port, but validate.go promises it loads: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(nodes))
	}

	// The node's own clock must be real and usable, not nil: this is the assertion
	// that makes the bug class unrepresentable rather than merely unobserved.
	// Per-goroutine-clock.md's API demolition removed In.Clock()/Out.Clock()
	// entirely (port accessors go away) — the node's OWN Clock field, seeded by
	// reflectBuild from the loader's origin, is what Update() Copies at its own
	// start instead, whether or not the In it also holds is wired.
	if got := nodes[0].(*Node).Clock; got == nil {
		t.Fatal("Node.Clock was nil for an unfed-port node — every pacing loop Copies it and calls SleepCycle unguarded")
	}

	// Production runs Update with no recover(); recover here so a regression is a test
	// failure instead of a dead test binary.
	panicked := make(chan any, 1)
	go func() {
		defer func() { panicked <- recover() }()
		nodes[0].Update(ctx)
	}()

	// Inert-and-alive: the loop paces on the shared clock and polls a port that never
	// delivers, so Update must neither panic nor return while ctx is live.
	select {
	case r := <-panicked:
		if r != nil {
			t.Fatalf("unfed node panicked instead of staying inert: %v", r)
		}
		t.Fatal("Update returned while ctx was still live — the node must stay inert, not exit")
	case <-time.After(200 * time.Millisecond):
		// Still running after many clock cycles: inert by precondition-gating.
	}

	// And it still shuts down cleanly.
	cancel()
	select {
	case r := <-panicked:
		if r != nil {
			t.Fatalf("unfed node panicked on shutdown: %v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("unfed node did not exit after ctx cancel")
	}
}
