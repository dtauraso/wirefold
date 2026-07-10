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

// A spatially-straight chain (grandparent → ref → node on one line) measures iTheta == 0
// for the node, since the triple is taken about the reference's incoming direction.
func TestSnapLocalStraightIsIThetaZero(t *testing.T) {
	dirv := polar2cart(polar{R: 1, Theta: 1.1, Phi: 0.4})
	centers := map[string]vec3{
		"g": {X: 0, Y: 0, Z: 0},
		"p": dirv.scale(50),  // g -> p along dirv
		"c": dirv.scale(110), // p -> c continues the SAME line
	}
	parent := map[string]string{"g": "", "p": "g", "c": "p"}
	got := snapQuantizedOffsets(centers, parent)
	if got["c"].iTheta != 0 {
		t.Fatalf("straight continuation should be iTheta=0, got %+v", got["c"])
	}
	// A bent node off the line is not iTheta=0.
	centers["c"] = centers["p"].add(polar2cart(polar{R: 60, Theta: 1.1 + 0.6, Phi: 0.4}))
	if got := snapQuantizedOffsets(centers, parent); got["c"].iTheta == 0 {
		t.Fatalf("bent node should not be iTheta=0, got %+v", got["c"])
	}
}

// snapToReference lands a dragged node exactly on the reference's incoming line when the
// target is near it (iTheta rounds to 0) — i.e. dragging makes it colinear.
func TestSnapToReferenceLandsColinear(t *testing.T) {
	dirv := polar2cart(polar{R: 1, Theta: 0.9, Phi: -0.5})
	md := &MoveDispatch{
		sceneSphere: sceneSphere{Center: vec3{}},
		references:  map[string]string{"g": "", "p": "g", "c": "p"},
		nodeMovers: map[string]*nodeMover{
			"g": {id: "g"}, "p": {id: "p"}, "c": {id: "c"},
		},
	}
	md.nodeMovers["g"].snap.Store(&centerSnap{c: vec3{}})
	md.nodeMovers["p"].snap.Store(&centerSnap{c: dirv.scale(50)})
	md.nodeMovers["c"].snap.Store(&centerSnap{c: dirv.scale(90)})

	// Target near the straight continuation but nudged off it.
	target := dirv.scale(100).add(vec3{X: 3, Y: -2, Z: 1})
	snapped, ok := md.snapToReference("c", target)
	if !ok {
		t.Fatal("expected a reference snap for a non-root node")
	}
	// Snapped point is colinear with g,p (cross(p-g, snapped-g) ~ 0).
	pG := dirv.scale(50)
	if cr := pG.cross(snapped).length(); cr > 1e-6 {
		t.Fatalf("snapped not colinear with the g→p line: cross=%v snapped=%v", cr, snapped)
	}
}
