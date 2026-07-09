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
	// Node ids are digits ("0","1","2") so the intended root ("0") is the lowest id in
	// the component — buildSpanningTree (used by computeQuantizedLayout / this test's own
	// direct call below) picks the lowest id per component as root.
	const topo = `{
	  "nodes": [
	    {"id":"0","type":"FanInSrc","scenePolarR":5,"scenePolarTheta":1.0,"scenePolarPhi":0.2,"outputs":[{"name":"Out"}]},
	    {"id":"1","type":"AimedPacer","quantITheta":1,"quantIPhi":2,"quantIR":1,"inputs":[{"name":"FromSrc"}],"outputs":[{"name":"Feedback"}]},
	    {"id":"2","type":"FanInSink","quantITheta":0,"quantIPhi":0,"quantIR":1,"inputs":[{"name":"In"}]}
	  ],
	  "edges": [
	    {"label":"e1","kind":"data","source":"0","sourceHandle":"Out","target":"1","targetHandle":"FromSrc"},
	    {"label":"e2","kind":"data","source":"1","sourceHandle":"Feedback","target":"2","targetHandle":"In"}
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
	if got := md.quantizedOffsets["1"]; got.iTheta != 1 || got.iPhi != 2 || got.iR != 1 || got.parent != "0" {
		t.Fatalf("quantizedOffsets[1] = %+v, want stored {1,2,1,0}", got)
	}
	if got := md.quantizedOffsets["2"]; got.parent != "1" {
		t.Fatalf("quantizedOffsets[2].parent = %q, want %q", got.parent, "1")
	}

	edgeEP := map[string]EdgeEndpoints{
		"e1": {Source: "0", Target: "1"},
		"e2": {Source: "1", Target: "2"},
	}
	parent, roots := buildSpanningTree(edgeEP)
	if !roots["0"] {
		t.Fatalf("expected 0 to be a root, roots=%v", roots)
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

	for _, id := range []string{"0", "1", "2"} {
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
	rootWant, _ := md.centerOfNode("0")
	if composed["0"].center != rootWant {
		t.Fatalf("root center = %v, want anchor %v", composed["0"].center, rootWant)
	}
}

// writeQuantTree lays down a 3-node directory-tree topology: root -> A -> B (a chain), with
// no stored quantized offsets, so computeQuantizedLayout falls back to snapping A/B from
// their loaded (default zero) scene-polar centers — i.e. root, A, B start COINCIDENT
// (iR==0 for both, since neither meta.json carries a position). This gives the drag test a
// clean, known starting point free of pre-existing offset noise.
// writeQuantTree's node ids are digits ("0","1","2") so the intended root ("0") is the
// lowest id in the component — buildSpanningTree picks the lowest id per component as
// root, so the ids themselves must encode the intended tree shape.
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
	mk("nodes/0/meta.json", `{"id":"0","type":"FanInSrc"}`)
	mk("nodes/0/outputs/Out.json", `{"name":"Out"}`)
	mk("nodes/1/meta.json", `{"id":"1","type":"AimedPacer"}`)
	mk("nodes/1/inputs/FromSrc.json", `{"name":"FromSrc"}`)
	mk("nodes/1/outputs/Feedback.json", `{"name":"Feedback"}`)
	mk("nodes/2/meta.json", `{"id":"2","type":"FanInSink"}`)
	mk("nodes/2/inputs/In.json", `{"name":"In"}`)
	if err := os.MkdirAll(filepath.Join(root, "edges"), 0o755); err != nil {
		t.Fatal(err)
	}
	mk("edges/e1.json", `{"label":"e1","kind":"data","source":"0","sourceHandle":"Out","target":"1","targetHandle":"FromSrc"}`)
	mk("edges/e2.json", `{"label":"e2","kind":"data","source":"1","sourceHandle":"Feedback","target":"2","targetHandle":"In"}`)
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

	bBefore, ok := md.centerOfNode("2")
	if !ok {
		t.Fatal("2 has no center before drag")
	}

	// stepR (== defaultNodeR, 200 world units) is large, so the target must be far enough
	// from the origin for its radial component to round to a NON-ZERO iR (otherwise "1" —
	// and the whole subtree hanging off it — would snap back to coincide with root "0").
	target := vec3{X: 330, Y: 510, Z: -200}
	if !md.RootMove("1", target) {
		t.Fatal("RootMove(1) returned false")
	}

	// (a) offset snapped to nearest grid integers about root's CURRENT composed forward.
	parentLayout := md.composeAll()["0"]
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
		parent: "0",
	}
	if got := md.quantizedOffsets["1"]; got != wantOff {
		t.Fatalf("quantizedOffsets[1] = %+v, want %+v", got, wantOff)
	}

	// (b) "1"'s new center == compose of the snapped offset.
	composed := md.composeAll()
	want1 := composed["1"].center
	waitCenterClose(t, md, "1", want1, 1e-6)

	// (c) "2" (the subtree hanging off "1") moved too — its new center matches the
	// recompose, and it is no longer at its pre-drag (coincident-with-root) position.
	wantB := composed["2"].center
	gotB := waitCenterClose(t, md, "2", wantB, 1e-6)
	if gotB.sub(bBefore).length() < 1e-6 {
		t.Fatalf("2 did not move with its parent 1's drag: before=%v after=%v", bBefore, gotB)
	}

	// (d) persisted: force the debounced writer to flush now rather than waiting out the
	// real debounce interval, then read "1"'s meta.json back.
	md.quantOffsetPersist.flush()
	raw, err := os.ReadFile(filepath.Join(root, "nodes", "1", "meta.json"))
	if err != nil {
		t.Fatalf("read 1 meta.json: %v", err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("unmarshal 1 meta.json: %v", err)
	}
	var gotITheta, gotIPhi, gotIR int
	_ = json.Unmarshal(obj["quantITheta"], &gotITheta)
	_ = json.Unmarshal(obj["quantIPhi"], &gotIPhi)
	_ = json.Unmarshal(obj["quantIR"], &gotIR)
	if gotITheta != wantOff.iTheta || gotIPhi != wantOff.iPhi || gotIR != wantOff.iR {
		t.Fatalf("1 meta.json quant offset = {%d,%d,%d}, want {%d,%d,%d}",
			gotITheta, gotIPhi, gotIR, wantOff.iTheta, wantOff.iPhi, wantOff.iR)
	}
}

// TestOldSceneSnapsOnLoad: a node with only a scenePolar (no stored quantized offset — an
// "old scene") gets its offset SNAPPED from its scenePolar-derived world center on load, and
// composing that snapped offset lands within one grid step of the node's original position
// (TestSnapComposeStable's tolerance convention).
func TestOldSceneSnapsOnLoad(t *testing.T) {
	// Node ids are digits ("0","1") so the intended root ("0") is the lowest id in the
	// component (buildSpanningTree picks the lowest id per component as root).
	const topo = `{
	  "nodes": [
	    {"id":"0","type":"FanInSrc","scenePolarR":0,"scenePolarTheta":0,"scenePolarPhi":0,"outputs":[{"name":"Out"}]},
	    {"id":"1","type":"FanInSink","scenePolarR":12.3,"scenePolarTheta":0.7,"scenePolarPhi":0.4,"inputs":[{"name":"In"}]}
	  ],
	  "edges": [
	    {"label":"e1","kind":"data","source":"0","sourceHandle":"Out","target":"1","targetHandle":"In"}
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

	if got := md.quantizedOffsets["1"].parent; got != "0" {
		t.Fatalf("quantizedOffsets[1].parent = %q, want %q", got, "0")
	}

	origA := polar2cart(polar{R: 12.3, Theta: 0.7, Phi: 0.4}) // "0" is at the scene origin
	gotA, ok := md.centerOfNode("1")
	if !ok {
		t.Fatal("1 has no center after load")
	}
	const posTol = stepR * 1.5 // TestSnapComposeStable's explicit one-grid-step tolerance
	if d := gotA.sub(origA).length(); d > posTol {
		t.Fatalf("1 center = %v, want within %v of original %v (delta %v)", gotA, posTol, origA, d)
	}
}
