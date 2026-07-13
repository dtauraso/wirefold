// layout_cascade_test.go — headless verification of the radius (c) drag cascade
// (docs/planning: task/drag-cascade-scope). Drives the real loader + the per-node
// layout goroutines and asserts the two-axis model:
//
//   - TRANSPORT: the dragged node seeds its new c onto its OWN out-edges; a node
//     forwards only if its kind == the message's PropagatingKind (the timer kind).
//   - UPDATE: a reached node repositions only if its kind is in UpdateKinds. Merely
//     receiving (or forwarding) the message does not move a node.
//   - Geometry is anchored on each node's OWN reference (never the sender), so the
//     message carries only the number c — no FromCenter.
//   - Termination is a fixpoint on c (no visited table): a node already at c stops.

package Wiring

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
)

// layoutTestNode is a minimal synthetic node used to exercise the cascade through
// the REAL loader/build path. Post the layout/bead goroutine split the cascade is
// drained by the per-node layout goroutine (LayoutPort.run, launched by
// MoveDispatch.Start) — NOT by the node's Update loop — so this Update is inert.
type layoutTestNode struct {
	Layout *LayoutPort
	In     *In
	Out    OutMulti
}

func (n *layoutTestNode) Update(ctx context.Context) { <-ctx.Done() }

func init() {
	// Unique test-only kind names so they never collide with the real kind
	// registrations other test files in this binary pull in via real node-package
	// imports. The cascade's participation kinds are configured on MoveDispatch
	// (md.timerKind / md.updateKinds), so the test drives the REAL cascade with
	// these synthetic names.
	Register("LayoutTestTime", func() any { return &layoutTestNode{} })
	Register("LayoutTestPlain", func() any { return &layoutTestNode{} })
}

// writeCascadeTree builds a small DIRECTED tree topology on disk (hidden layout
// edges mirror domain edges source->target one-for-one):
//
//	2 (LayoutTestTime, root)
//	└─ 5 (LayoutTestTime, ref 2)        [dragged]
//	   ├─ 7 (LayoutTestPlain, ref 5)    receives, does NOT forward, does NOT move
//	   │  └─ 10 (LayoutTestPlain, ref 7) never reached (7 doesn't forward)
//	   └─ 8 (LayoutTestTime, ref 5)     timer: moves AND forwards
//	      └─ 9 (LayoutTestPlain, ref 8) receives (via 8), does NOT move
func writeCascadeTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mk := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}

	mk("nodes/2/meta.json", `{"id":"2","type":"LayoutTestTime","quantITheta":0,"quantIPhi":0,"quantIR":0}`)
	mk("nodes/2/inputs/In.json", `{"name":"In"}`)
	mk("nodes/2/outputs/Out0.json", `{"name":"Out0"}`)

	mk("nodes/5/meta.json", `{"id":"5","type":"LayoutTestTime","reference":"2","quantITheta":1,"quantIPhi":0,"quantIR":1}`)
	mk("nodes/5/inputs/In.json", `{"name":"In"}`)
	mk("nodes/5/outputs/Out0.json", `{"name":"Out0"}`)
	mk("nodes/5/outputs/Out1.json", `{"name":"Out1"}`)

	mk("nodes/7/meta.json", `{"id":"7","type":"LayoutTestPlain","reference":"5","quantITheta":0,"quantIPhi":1,"quantIR":1}`)
	mk("nodes/7/inputs/In.json", `{"name":"In"}`)
	mk("nodes/7/outputs/Out0.json", `{"name":"Out0"}`)

	mk("nodes/10/meta.json", `{"id":"10","type":"LayoutTestPlain","reference":"7","quantITheta":0,"quantIPhi":0,"quantIR":1}`)
	mk("nodes/10/inputs/In.json", `{"name":"In"}`)

	mk("nodes/8/meta.json", `{"id":"8","type":"LayoutTestTime","reference":"5","quantITheta":-1,"quantIPhi":0,"quantIR":1}`)
	mk("nodes/8/inputs/In.json", `{"name":"In"}`)
	mk("nodes/8/outputs/Out0.json", `{"name":"Out0"}`)

	mk("nodes/9/meta.json", `{"id":"9","type":"LayoutTestPlain","reference":"8","quantITheta":0,"quantIPhi":0,"quantIR":1}`)
	mk("nodes/9/inputs/In.json", `{"name":"In"}`)

	if err := os.MkdirAll(filepath.Join(root, "edges"), 0o755); err != nil {
		t.Fatal(err)
	}
	mk("edges/2To5.json", `{"label":"2To5","kind":"data","source":"2","sourceHandle":"Out0","target":"5","targetHandle":"In"}`)
	mk("edges/5To7.json", `{"label":"5To7","kind":"data","source":"5","sourceHandle":"Out0","target":"7","targetHandle":"In"}`)
	mk("edges/5To8.json", `{"label":"5To8","kind":"data","source":"5","sourceHandle":"Out1","target":"8","targetHandle":"In"}`)
	mk("edges/7To10.json", `{"label":"7To10","kind":"data","source":"7","sourceHandle":"Out0","target":"10","targetHandle":"In"}`)
	mk("edges/8To9.json", `{"label":"8To9","kind":"data","source":"8","sourceHandle":"Out0","target":"9","targetHandle":"In"}`)

	if err := os.MkdirAll(filepath.Join(root, "view", "nodes"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// readPersistedQuantIR polls <root>/nodes/<id>/meta.json until its quantIR field
// equals want (or fails after a bounded budget) — the persister debounces off the
// drag. Reading disk is race-free (the layout goroutine owns the in-memory iR).
func readPersistedQuantIR(t *testing.T, root, id string, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var last int
	for time.Now().Before(deadline) {
		if v, ok := readQuantIR(root, id); ok {
			last = v
			if last == want {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("node %s: meta.json quantIR never reached %d (last seen %d)", id, want, last)
}

// assertQuantIRStable reads a node's persisted quantIR ONCE after the cascade has
// converged and asserts it equals want — used to prove a node did NOT move.
func assertQuantIRStable(t *testing.T, root, id string, want int) {
	t.Helper()
	v, ok := readQuantIR(root, id)
	if !ok {
		t.Fatalf("node %s: meta.json has no quantIR", id)
	}
	if v != want {
		t.Fatalf("node %s: quantIR moved to %d, expected it to stay %d", id, v, want)
	}
}

func readQuantIR(root, id string) (int, bool) {
	raw, err := os.ReadFile(filepath.Join(root, "nodes", id, "meta.json"))
	if err != nil {
		return 0, false
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return 0, false
	}
	if f, ok := m["quantIR"].(float64); ok {
		return int(f), true
	}
	return 0, false
}

// TestRadiusCascadeUpdatesTimersReachableOnly drives a live drag on node "5" and
// asserts the two-axis model on the directed tree: 5 seeds its new c onto its own
// out-edges {7,8}; only the timer kind forwards, only UpdateKinds move.
//   - 8 (timer, in UpdateKinds): reached via 5->8 → MOVES, and forwards to 9.
//   - 7 (plain): reached via 5->7 → does NOT move and does NOT forward, so 10 is
//     never reached.
//   - 9 (plain): reached via 8->9 (forwarded) → does NOT move.
//   - 2 (upstream): no 5->2 edge in this directed tree → never reached.
func TestRadiusCascadeUpdatesTimersReachableOnly(t *testing.T) {
	root := writeCascadeTree(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, _, md, err := LoadTopology(ctx, root, T.New(1024), NewRealClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}
	// Drive the cascade with the synthetic kinds: only LayoutTestTime propagates,
	// and only LayoutTestTime updates (so plain nodes reached by the wave must NOT
	// move — exercising the update gate).
	md.timerKind = "LayoutTestTime"
	md.updateKinds = map[string]bool{"LayoutTestTime": true}
	md.EnableEditPersist(root)
	md.Start(ctx)

	// Drag "5" to a NEW radius along its existing angle about reference "2".
	refCenter, ok := md.centerOfNode("2")
	if !ok {
		t.Fatal("2 has no center before drag")
	}
	const newIRWant = 3 // loaded iR of 5 is 1
	target := refCenter.add(polar2cart(polar{R: float64(newIRWant) * stepR, Theta: 1 * stepTheta, Phi: 0}))
	if !md.RootMove("5", target) {
		t.Fatal("RootMove(5) returned false")
	}
	newIR := md.quantizedOffsets["5"].iR

	// 8 is a timer reached via 5->8 → it adopts the new c (proves propagation +
	// the update gate admitting a timer + fixpoint termination, no hang).
	readPersistedQuantIR(t, root, "8", newIR)

	// Everything else stays at its loaded iR (1): 7 and 9 are reached but are plain
	// (not in UpdateKinds) so they don't move; 10 is behind non-forwarding 7 so it
	// is never reached; 2 is upstream (no 5->2 edge).
	assertQuantIRStable(t, root, "7", 1)
	assertQuantIRStable(t, root, "9", 1)
	assertQuantIRStable(t, root, "10", 1)
	assertQuantIRStable(t, root, "2", 0)
}
