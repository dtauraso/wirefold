package Wiring

import (
	"math"
	"testing"
)

const quantEps = 1e-9

// parallel checks whether two vec3s point the same direction (allowing different
// magnitudes but not opposite sign) within eps, using the normalized-dot-product test.
func parallel(t *testing.T, a, b vec3, msg string) {
	t.Helper()
	al, bl := a.length(), b.length()
	if al < quantEps || bl < quantEps {
		t.Fatalf("%s: degenerate vector(s) a=%v (len %v) b=%v (len %v)", msg, a, al, b, bl)
	}
	d := a.normalize().dot(b.normalize())
	if math.Abs(d-1) > quantEps {
		t.Fatalf("%s: not parallel (dot=%v), a=%v b=%v", msg, d, a, b)
	}
}

func TestComposeStraightChainColinear(t *testing.T) {
	// Parent/roots built EXPLICITLY (bypassing buildSpanningTree) so this test's intended
	// root (R) doesn't depend on buildSpanningTree's lowest-id-per-component rule — this
	// test is about the compose forward-kinematics math, not tree-building.
	parent := map[string]string{"R": "", "A": "R", "B": "A"}
	roots := map[string]bool{"R": true}
	offsets := map[string]quantizedOffset{
		"A": {iTheta: 0, iPhi: 0, iR: 1},
		"B": {iTheta: 0, iPhi: 0, iR: 1},
	}
	anchor := vec3{X: 0, Y: 0, Z: 0}
	layout := composeQuantizedLayout(parent, roots, offsets, anchor)

	r, a, b := layout["R"], layout["A"], layout["B"]
	if r.center != anchor {
		t.Fatalf("root center = %v, want anchor %v", r.center, anchor)
	}
	vAminusR := a.center.sub(r.center)
	vBminusA := b.center.sub(a.center)
	parallel(t, vAminusR, vBminusA, "straight chain: B-A should be parallel to A-R")
}

func TestComposeSiblingsSameOffsetColinear(t *testing.T) {
	// Parent/roots built explicitly (see TestComposeStraightChainColinear).
	parent := map[string]string{"P": "", "X": "P", "Y": "P"}
	roots := map[string]bool{"P": true}
	offsets := map[string]quantizedOffset{
		"X": {iTheta: 1, iPhi: 1, iR: 1},
		"Y": {iTheta: 1, iPhi: 1, iR: 2}, // different radial step, same angular offset
	}
	anchor := vec3{X: 10, Y: 20, Z: 30}
	layout := composeQuantizedLayout(parent, roots, offsets, anchor)

	p, x, y := layout["P"], layout["X"], layout["Y"]
	vPX := x.center.sub(p.center)
	vPY := y.center.sub(p.center)
	parallel(t, vPX, vPY, "siblings with same angular offset should lie on one ray from P")
}

func TestComposeRotationalNesting(t *testing.T) {
	// Parent/roots built explicitly (see TestComposeStraightChainColinear).
	parent := map[string]string{"R": "", "A": "R", "B": "A"}
	roots := map[string]bool{"R": true}
	offsets := map[string]quantizedOffset{
		"A": {iTheta: 1, iPhi: 0, iR: 1}, // A bends off R's forward
		"B": {iTheta: 0, iPhi: 0, iR: 1}, // B continues straight along A's (bent) forward
	}
	anchor := vec3{X: 0, Y: 0, Z: 0}
	layout := composeQuantizedLayout(parent, roots, offsets, anchor)

	r, a, b := layout["R"], layout["A"], layout["B"]

	if a.forward == r.forward {
		t.Fatalf("A's forward should differ from R's forward (iTheta=1 bends it), got equal %v", a.forward)
	}

	// B's position minus A's position should be parallel to A's forward direction.
	vAB := b.center.sub(a.center)
	aFwdCart := cart(a.forward)
	parallel(t, vAB, aFwdCart, "B-A should be parallel to A's (bent) forward direction")

	_ = r
}

// TestSpanningTreeRootIsLowestIdPerComponent asserts the NEW buildSpanningTree rule: per
// weakly-connected component, the root is the lowest-id node (string sort), parents are
// assigned by BFS from that root, the result is acyclic, and every node in the component
// is reachable.
func TestSpanningTreeRootIsLowestIdPerComponent(t *testing.T) {
	edges := map[string]EdgeEndpoints{
		"b->c": {Source: "b", Target: "c"},
		"a->c": {Source: "a", Target: "c"},
		"d->e": {Source: "d", Target: "e"},
	}
	parent, roots := buildSpanningTree(edges)

	// One component {a,b,c}: lowest id "a" is the root.
	if !roots["a"] {
		t.Fatalf("expected a (lowest id) to be a root, roots=%v", roots)
	}
	if roots["b"] || roots["c"] {
		t.Fatalf("only the lowest id per component should be a root, roots=%v", roots)
	}
	// Another component {d,e}: lowest id "d" is the root.
	if !roots["d"] {
		t.Fatalf("expected d (lowest id) to be a root, roots=%v", roots)
	}
	if roots["e"] {
		t.Fatalf("e should not be a root, roots=%v", roots)
	}

	// Every node reachable / covered.
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		if _, ok := parent[id]; !ok {
			t.Fatalf("node %q missing from parent map, parent=%v", id, parent)
		}
	}

	// Acyclic: walking parent pointers from any node terminates at a root without
	// revisiting a node.
	for _, start := range []string{"a", "b", "c", "d", "e"} {
		seen := map[string]bool{}
		cur := start
		for cur != "" {
			if seen[cur] {
				t.Fatalf("cycle detected in parent chain starting at %q", start)
			}
			seen[cur] = true
			cur = parent[cur]
		}
	}
}

func TestMoveDispatchComposeQuantizedLayoutGuarded(t *testing.T) {
	edges := map[string]EdgeEndpoints{
		"R->A": {Source: "R", Target: "A"},
	}
	parent, _ := buildSpanningTree(edges)
	offsets := quantizedOffsetsFromParents(parent)
	aOff := offsets["A"]
	aOff.iR = 1
	offsets["A"] = aOff

	md := &MoveDispatch{sceneSphere: sceneSphere{Center: vec3{X: 1, Y: 2, Z: 3}}}
	md.quantizedOffsets = offsets

	// Guard off by default: nothing composed.
	if got := md.ComposeQuantizedLayout(); got != nil {
		t.Fatalf("expected nil while quantizedLayout guard is off, got %v", got)
	}

	md.quantizedLayout = true
	got := md.ComposeQuantizedLayout()
	if got["R"].center != md.sceneSphere.Center {
		t.Fatalf("root center = %v, want scene sphere center %v", got["R"].center, md.sceneSphere.Center)
	}
}

// TestSpanningTreeFullyBidirectionalHasRoot is the regression test for the bug this fix
// addresses: a FULLY bidirectional graph (every edge present both directions, so no node
// has zero in-degree under the old directed rule) must still produce exactly one root
// (the lowest id), every other node must have a parent, the parent chain must be acyclic,
// and composeQuantizedLayout must return a center for EVERY node (not empty) — this is
// the exact condition that froze dragging.
func TestSpanningTreeFullyBidirectionalHasRoot(t *testing.T) {
	edges := map[string]EdgeEndpoints{
		"1->2": {Source: "1", Target: "2"},
		"2->1": {Source: "2", Target: "1"},
		"2->3": {Source: "2", Target: "3"},
		"3->2": {Source: "3", Target: "2"},
		"1->3": {Source: "1", Target: "3"},
		"3->1": {Source: "3", Target: "1"},
	}
	parent, roots := buildSpanningTree(edges)

	if len(roots) != 1 {
		t.Fatalf("expected exactly one root, got roots=%v", roots)
	}
	if !roots["1"] {
		t.Fatalf("expected root to be lowest id %q, roots=%v", "1", roots)
	}
	for _, id := range []string{"2", "3"} {
		if parent[id] == "" {
			t.Fatalf("expected node %q to have a non-root parent, parent=%v", id, parent)
		}
	}

	// Acyclic parent chain.
	for _, start := range []string{"1", "2", "3"} {
		seen := map[string]bool{}
		cur := start
		for cur != "" {
			if seen[cur] {
				t.Fatalf("cycle detected in parent chain starting at %q", start)
			}
			seen[cur] = true
			cur = parent[cur]
		}
	}

	offsets := quantizedOffsetsFromParents(parent)
	composed := composeQuantizedLayout(parent, roots, offsets, vec3{})
	for _, id := range []string{"1", "2", "3"} {
		if _, ok := composed[id]; !ok {
			t.Fatalf("composeQuantizedLayout missing center for %q, composed=%v", id, composed)
		}
	}
}

// TestSpanningTreeMultipleComponents: two disconnected bidirectional clusters produce two
// roots, each the lowest id within its own cluster.
func TestSpanningTreeMultipleComponents(t *testing.T) {
	edges := map[string]EdgeEndpoints{
		"a->b": {Source: "a", Target: "b"},
		"b->a": {Source: "b", Target: "a"},
		"x->y": {Source: "x", Target: "y"},
		"y->x": {Source: "y", Target: "x"},
	}
	_, roots := buildSpanningTree(edges)
	want := map[string]bool{"a": true, "x": true}
	for id := range want {
		if !roots[id] {
			t.Fatalf("expected %q to be a root, roots=%v", id, roots)
		}
	}
	for id := range roots {
		if !want[id] {
			t.Fatalf("unexpected root %q, roots=%v", id, roots)
		}
	}
}

// TestSnapRecoversExactGridLayout is the strongest correctness check for PHASE 2: build a
// layout with KNOWN integer offsets via composeQuantizedLayout, then run
// snapQuantizedOffsets on the resulting centers and assert it recovers the SAME integers
// exactly. This is the round-trip snap(compose(offsets)) == offsets on grid-aligned input.
func TestSnapRecoversExactGridLayout(t *testing.T) {
	// Node ids are single digits ("0","1",...) chosen so the intended root ("0"/"P"→"0")
	// is the LOWEST id in its component — buildSpanningTree (called internally by
	// snapQuantizedOffsets) picks the lowest-id node per component as root, so the ids
	// must encode the intended tree shape, not just label it in edge names.
	t.Run("3-deep chain", func(t *testing.T) {
		edges := map[string]EdgeEndpoints{
			"0->1": {Source: "0", Target: "1"},
			"1->2": {Source: "1", Target: "2"},
			"2->3": {Source: "2", Target: "3"},
		}
		// iTheta is a colatitude-like offset (always >= 0 by construction: it comes from
		// angularDistance/acos, which never returns negative) — a negative iTheta would
		// alias with (-iTheta, iPhi+π/stepPhi) representing the identical direction, so
		// round-trip test data must stick to iTheta >= 0. Likewise iTheta == 0 (straight
		// continuation along the pole) makes iPhi (bearing) undefined/degenerate — atan2
		// resolves it to 0 regardless of the authored value — so round-trip test data
		// must keep iTheta != 0 wherever iPhi is meant to be recovered.
		want := map[string]quantizedOffset{
			"1": {iTheta: 2, iPhi: -1, iR: 1, parent: "0"},
			"2": {iTheta: 1, iPhi: 3, iR: 2, parent: "1"},
			"3": {iTheta: 1, iPhi: 2, iR: 1, parent: "2"},
		}
		parent, roots := buildSpanningTree(edges)
		full := map[string]quantizedOffset{"0": {parent: ""}}
		for id, o := range want {
			full[id] = o
		}
		anchor := vec3{X: 5, Y: -3, Z: 7}
		layout := composeQuantizedLayout(parent, roots, full, anchor)
		centers := map[string]vec3{}
		for id, l := range layout {
			centers[id] = l.center
		}

		got := snapQuantizedOffsets(centers, edges)
		for id, o := range want {
			g, ok := got[id]
			if !ok {
				t.Fatalf("snap missing offset for %q, got=%v", id, got)
			}
			if g != o {
				t.Fatalf("snap[%q] = %+v, want %+v", id, g, o)
			}
		}
		if g := got["0"]; g.parent != "" || g.iTheta != 0 || g.iPhi != 0 || g.iR != 0 {
			t.Fatalf("root offset = %+v, want zero offset with no parent", g)
		}
	})

	t.Run("branching node", func(t *testing.T) {
		edges := map[string]EdgeEndpoints{
			"0->1": {Source: "0", Target: "1"},
			"0->2": {Source: "0", Target: "2"},
			"0->3": {Source: "0", Target: "3"},
		}
		want := map[string]quantizedOffset{
			"1": {iTheta: 1, iPhi: 0, iR: 1, parent: "0"},
			"2": {iTheta: 1, iPhi: 4, iR: 1, parent: "0"},
			"3": {iTheta: 2, iPhi: -3, iR: 2, parent: "0"},
		}
		parent, roots := buildSpanningTree(edges)
		full := map[string]quantizedOffset{"0": {parent: ""}}
		for id, o := range want {
			full[id] = o
		}
		anchor := vec3{X: 0, Y: 0, Z: 0}
		layout := composeQuantizedLayout(parent, roots, full, anchor)
		centers := map[string]vec3{}
		for id, l := range layout {
			centers[id] = l.center
		}

		got := snapQuantizedOffsets(centers, edges)
		for id, o := range want {
			g, ok := got[id]
			if !ok {
				t.Fatalf("snap missing offset for %q, got=%v", id, got)
			}
			if g != o {
				t.Fatalf("snap[%q] = %+v, want %+v", id, g, o)
			}
		}
	})
}

// TestSnapComposeStable checks the fixpoint property on ARBITRARY (non-grid-aligned)
// centers: snapQuantizedOffsets(centers) then composeQuantizedLayout(snapped) should land
// within one grid step of the input (snapping rounds each node's offset to the nearest
// grid point, so recomposing lands near, not exactly on, the original arbitrary center).
func TestSnapComposeStable(t *testing.T) {
	// Node ids are digits so the intended root ("0") is the lowest id in its component
	// (buildSpanningTree, called internally by snapQuantizedOffsets, picks the lowest id
	// per component as root).
	edges := map[string]EdgeEndpoints{
		"0->1": {Source: "0", Target: "1"},
		"1->2": {Source: "1", Target: "2"},
	}
	anchor := vec3{X: 0, Y: 0, Z: 0}
	// Arbitrary (not grid-aligned) centers, reachable from anchor via "0".
	centers := map[string]vec3{
		"0": anchor,
		"1": vec3{X: 3.1, Y: 0.4, Z: -2.2},
		"2": vec3{X: 5.9, Y: 1.7, Z: -3.8},
	}

	snapped := snapQuantizedOffsets(centers, edges)
	parent, roots := buildSpanningTree(edges)
	recomposed := composeQuantizedLayout(parent, roots, snapped, anchor)

	// One grid step tolerance: the maximum positional error introduced by rounding
	// iTheta/iPhi/iR by up to 0.5 step each, generously bounded by stepR (the radial
	// step) plus a slack factor for the angular rounding's arc-length contribution at
	// this radius. Use a concrete, explicit tolerance rather than a vague "close".
	const posTol = stepR * 1.5

	for id, want := range centers {
		got, ok := recomposed[id]
		if !ok {
			t.Fatalf("recompose missing %q", id)
		}
		d := got.center.sub(want).length()
		if d > posTol {
			t.Fatalf("recompose[%q] = %v, want within %v of %v (delta %v)", id, got.center, posTol, want, d)
		}
	}
}
