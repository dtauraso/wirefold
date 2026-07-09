// quantized_layout_phase3_test.go — PHASE 3: the quantized layout is AUTHORITATIVE for
// node positions and dragging snaps to the grid (see quantized_layout.go doc comments and
// loader.go computeQuantizedLayout / node_move.go rootMoveQuantized).

package Wiring

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
)

// waitCenterClose polls md.centerOfNode(id) until it is within tol of want, or fails after
// a bounded budget — the movers apply a RootMove asynchronously (mover goroutines), so a
// freshly-issued drag's center is not necessarily visible the instant RootMove returns.
func waitCenterClose(t *testing.T, md *MoveDispatch, id string, want vec3, tol float64) vec3 {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var got vec3
	for {
		if c, ok := md.centerOfNode(id); ok {
			got = c
			if c.sub(want).length() <= tol {
				return c
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("node %s never reached %v (last %v)", id, want, got)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestLoadComposesAuthoritative: compose (composeQuantizedLayoutAnchored, anchored at each
// root's OWN loaded center) is the source of truth for every node's emitted/held center —
// asserted directly against md.quantizedOffsets + md.centerOfNode after a real LoadTopology.
func TestLoadComposesAuthoritative(t *testing.T) {
	const topo = `{
	  "nodes": [
	    {"id":"R","type":"FanInSrc","scenePolarR":5,"scenePolarTheta":1.0,"scenePolarPhi":0.2,"outputs":[{"name":"Out"}]},
	    {"id":"A","type":"AimedPacer","quantITheta":1,"quantIPhi":2,"quantIR":1,"inputs":[{"name":"FromSrc"}],"outputs":[{"name":"Feedback"}]},
	    {"id":"B","type":"FanInSink","quantITheta":0,"quantIPhi":0,"quantIR":1,"inputs":[{"name":"In"}]}
	  ],
	  "edges": [
	    {"label":"e1","kind":"data","source":"R","sourceHandle":"Out","target":"A","targetHandle":"FromSrc"},
	    {"label":"e2","kind":"data","source":"A","sourceHandle":"Feedback","target":"B","targetHandle":"In"}
	  ]
	}`
	dir := t.TempDir()
	path := filepath.Join(dir, "topo.json")
	if err := os.WriteFile(path, []byte(topo), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr := T.New(256)
	_, _, md, err := LoadTopology(ctx, path, tr, NewFakeClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}

	if !md.quantizedLayout {
		t.Fatal("expected md.quantizedLayout to default true (Phase 3 authoritative)")
	}
	if got := md.quantizedOffsets["A"]; got.iTheta != 1 || got.iPhi != 2 || got.iR != 1 || got.parent != "R" {
		t.Fatalf("quantizedOffsets[A] = %+v, want stored {1,2,1,R}", got)
	}
	if got := md.quantizedOffsets["B"]; got.parent != "A" {
		t.Fatalf("quantizedOffsets[B].parent = %q, want %q", got.parent, "A")
	}

	edgeEP := map[string]EdgeEndpoints{
		"e1": {Source: "R", Target: "A"},
		"e2": {Source: "A", Target: "B"},
	}
	parent, roots := buildSpanningTree(edgeEP)
	if !roots["R"] {
		t.Fatalf("expected R to be a root, roots=%v", roots)
	}
	anchors := map[string]vec3{}
	for id := range roots {
		c, ok := md.centerOfNode(id)
		if !ok {
			t.Fatalf("root %s has no center", id)
		}
		anchors[id] = c
	}
	composed := composeQuantizedLayoutAnchored(parent, roots, md.quantizedOffsets, anchors)

	for _, id := range []string{"R", "A", "B"} {
		want, ok := composed[id]
		if !ok {
			t.Fatalf("compose missing %q", id)
		}
		got, ok := md.centerOfNode(id)
		if !ok {
			t.Fatalf("node %s has no center", id)
		}
		if d := got.sub(want.center).length(); d > 1e-9 {
			t.Fatalf("node %s center = %v, want composed %v (delta %v)", id, got, want.center, d)
		}
	}

	// The root itself sits exactly at its own anchor (its loaded center — unchanged).
	rootWant, _ := md.centerOfNode("R")
	if composed["R"].center != rootWant {
		t.Fatalf("root center = %v, want anchor %v", composed["R"].center, rootWant)
	}
}

// writeQuantTree lays down a 3-node directory-tree topology: root -> A -> B (a chain), with
// no stored quantized offsets, so computeQuantizedLayout falls back to snapping A/B from
// their loaded (default zero) scene-polar centers — i.e. root, A, B start COINCIDENT
// (iR==0 for both, since neither meta.json carries a position). This gives the drag test a
// clean, known starting point free of pre-existing offset noise.
func writeQuantTree(t *testing.T) string {
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
	mk("nodes/root/meta.json", `{"id":"root","type":"FanInSrc"}`)
	mk("nodes/root/outputs/Out.json", `{"name":"Out"}`)
	mk("nodes/A/meta.json", `{"id":"A","type":"AimedPacer"}`)
	mk("nodes/A/inputs/FromSrc.json", `{"name":"FromSrc"}`)
	mk("nodes/A/outputs/Feedback.json", `{"name":"Feedback"}`)
	mk("nodes/B/meta.json", `{"id":"B","type":"FanInSink"}`)
	mk("nodes/B/inputs/In.json", `{"name":"In"}`)
	if err := os.MkdirAll(filepath.Join(root, "edges"), 0o755); err != nil {
		t.Fatal(err)
	}
	mk("edges/e1.json", `{"label":"e1","kind":"data","source":"root","sourceHandle":"Out","target":"A","targetHandle":"FromSrc"}`)
	mk("edges/e2.json", `{"label":"e2","kind":"data","source":"A","sourceHandle":"Feedback","target":"B","targetHandle":"In"}`)
	if err := os.MkdirAll(filepath.Join(root, "view", "nodes"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestDragSnapsToGridAndMovesSubtree drags A (root's child, with B hanging off A) to an
// arbitrary world target and asserts: (a) A's offset snaps to the nearest grid integers
// about root's forward, (b) A's new center equals the compose of that snapped offset,
// (c) B (the subtree) moves too — rotational nesting, and (d) the snapped offset is
// persisted to A's meta.json.
func TestDragSnapsToGridAndMovesSubtree(t *testing.T) {
	root := writeQuantTree(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr := T.New(256)
	_, _, md, err := LoadTopology(ctx, root, tr, NewFakeClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}
	md.EnableEditPersist(root)
	md.Start(ctx)

	bBefore, ok := md.centerOfNode("B")
	if !ok {
		t.Fatal("B has no center before drag")
	}

	// stepR (== defaultNodeR, 200 world units) is large, so the target must be far enough
	// from the origin for its radial component to round to a NON-ZERO iR (otherwise A —
	// and the whole subtree hanging off it — would snap back to coincide with root).
	target := vec3{X: 330, Y: 510, Z: -200}
	if !md.RootMove("A", target) {
		t.Fatal("RootMove(A) returned false")
	}

	// (a) offset snapped to nearest grid integers about root's CURRENT composed forward.
	parentLayout := md.composeAll()["root"]
	delta := target.sub(parentLayout.center)
	r := delta.length()
	childDir := dir{}
	if r > 0 {
		p := cart2polar(delta)
		childDir = dir{Theta: p.Theta, Phi: p.Phi}
	}
	c, psi := azimuthFrom(parentLayout.forward, childDir)
	wantOff := quantizedOffset{
		iTheta: int(math.Round(c / stepTheta)),
		iPhi:   int(math.Round(psi / stepPhi)),
		iR:     int(math.Round(r / stepR)),
		parent: "root",
	}
	if got := md.quantizedOffsets["A"]; got != wantOff {
		t.Fatalf("quantizedOffsets[A] = %+v, want %+v", got, wantOff)
	}

	// (b) A's new center == compose of the snapped offset.
	composed := md.composeAll()
	wantA := composed["A"].center
	waitCenterClose(t, md, "A", wantA, 1e-6)

	// (c) B (the subtree hanging off A) moved too — its new center matches the recompose,
	// and it is no longer at its pre-drag (coincident-with-root) position.
	wantB := composed["B"].center
	gotB := waitCenterClose(t, md, "B", wantB, 1e-6)
	if gotB.sub(bBefore).length() < 1e-6 {
		t.Fatalf("B did not move with its parent A's drag: before=%v after=%v", bBefore, gotB)
	}

	// (d) persisted: force the debounced writer to flush now rather than waiting out the
	// real debounce interval, then read A's meta.json back.
	md.quantOffsetPersist.flush()
	raw, err := os.ReadFile(filepath.Join(root, "nodes", "A", "meta.json"))
	if err != nil {
		t.Fatalf("read A meta.json: %v", err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("unmarshal A meta.json: %v", err)
	}
	var gotITheta, gotIPhi, gotIR int
	_ = json.Unmarshal(obj["quantITheta"], &gotITheta)
	_ = json.Unmarshal(obj["quantIPhi"], &gotIPhi)
	_ = json.Unmarshal(obj["quantIR"], &gotIR)
	if gotITheta != wantOff.iTheta || gotIPhi != wantOff.iPhi || gotIR != wantOff.iR {
		t.Fatalf("A meta.json quant offset = {%d,%d,%d}, want {%d,%d,%d}",
			gotITheta, gotIPhi, gotIR, wantOff.iTheta, wantOff.iPhi, wantOff.iR)
	}
}

// TestOldSceneSnapsOnLoad: a node with only a scenePolar (no stored quantized offset — an
// "old scene") gets its offset SNAPPED from its scenePolar-derived world center on load, and
// composing that snapped offset lands within one grid step of the node's original position
// (TestSnapComposeStable's tolerance convention).
func TestOldSceneSnapsOnLoad(t *testing.T) {
	const topo = `{
	  "nodes": [
	    {"id":"R","type":"FanInSrc","scenePolarR":0,"scenePolarTheta":0,"scenePolarPhi":0,"outputs":[{"name":"Out"}]},
	    {"id":"A","type":"FanInSink","scenePolarR":12.3,"scenePolarTheta":0.7,"scenePolarPhi":0.4,"inputs":[{"name":"In"}]}
	  ],
	  "edges": [
	    {"label":"e1","kind":"data","source":"R","sourceHandle":"Out","target":"A","targetHandle":"In"}
	  ]
	}`
	dir := t.TempDir()
	path := filepath.Join(dir, "topo.json")
	if err := os.WriteFile(path, []byte(topo), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr := T.New(256)
	_, _, md, err := LoadTopology(ctx, path, tr, NewFakeClock())
	if err != nil {
		t.Fatalf("LoadTopology: %v", err)
	}

	if got := md.quantizedOffsets["A"].parent; got != "R" {
		t.Fatalf("quantizedOffsets[A].parent = %q, want %q", got, "R")
	}

	origA := polar2cart(polar{R: 12.3, Theta: 0.7, Phi: 0.4}) // R is at the scene origin
	gotA, ok := md.centerOfNode("A")
	if !ok {
		t.Fatal("A has no center after load")
	}
	const posTol = stepR * 1.5 // TestSnapComposeStable's explicit one-grid-step tolerance
	if d := gotA.sub(origA).length(); d > posTol {
		t.Fatalf("A center = %v, want within %v of original %v (delta %v)", gotA, posTol, origA, d)
	}
}
