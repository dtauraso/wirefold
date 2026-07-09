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
	edges := map[string]EdgeEndpoints{
		"R->A": {Source: "R", Target: "A"},
		"A->B": {Source: "A", Target: "B"},
	}
	parent, roots := buildSpanningTree(edges)
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
	edges := map[string]EdgeEndpoints{
		"P->X": {Source: "P", Target: "X"},
		"P->Y": {Source: "P", Target: "Y"},
	}
	parent, roots := buildSpanningTree(edges)
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
	edges := map[string]EdgeEndpoints{
		"R->A": {Source: "R", Target: "A"},
		"A->B": {Source: "A", Target: "B"},
	}
	parent, roots := buildSpanningTree(edges)
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

func TestComposeParentIsLowestIdSource(t *testing.T) {
	// Multi-edge graph: C has incoming edges from both "b" and "a" (lowest id "a" wins).
	// Bidirectional pair D<->E: D's parent should be "" (no incoming aside from E, but
	// E->D exists) except E also has D->E, so both have exactly one incoming edge from
	// the other — parent(D)="E", parent(E)="D" is a 2-cycle; the tree-walk cycle guard
	// handles that separately. Here we only check the local parent-selection rule.
	edges := map[string]EdgeEndpoints{
		"b->c": {Source: "b", Target: "c"},
		"a->c": {Source: "a", Target: "c"},
		"d->e": {Source: "d", Target: "e"},
		"e->d": {Source: "e", Target: "d"},
	}
	parent, roots := buildSpanningTree(edges)

	if parent["c"] != "a" {
		t.Fatalf("parent[c] = %q, want %q (lowest-id source)", parent["c"], "a")
	}
	// Bidirectional pair: each names the other as parent (single parent per node, per
	// spec — "bidirectional pairs use that single parent").
	if parent["d"] != "e" {
		t.Fatalf("parent[d] = %q, want %q", parent["d"], "e")
	}
	if parent["e"] != "d" {
		t.Fatalf("parent[e] = %q, want %q", parent["e"], "d")
	}
	// a and b have no incoming edges at all -> roots.
	if !roots["a"] {
		t.Fatalf("expected a to be a root")
	}
	if !roots["b"] {
		t.Fatalf("expected b to be a root")
	}
	if roots["c"] {
		t.Fatalf("c has an incoming edge, should not be a root")
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

func TestComposeRootsNoIncoming(t *testing.T) {
	edges := map[string]EdgeEndpoints{
		"R1->A": {Source: "R1", Target: "A"},
		"R2->B": {Source: "R2", Target: "B"},
		"A->C":  {Source: "A", Target: "C"},
	}
	_, roots := buildSpanningTree(edges)
	want := map[string]bool{"R1": true, "R2": true}
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
	t.Run("3-deep chain", func(t *testing.T) {
		edges := map[string]EdgeEndpoints{
			"R->A": {Source: "R", Target: "A"},
			"A->B": {Source: "A", Target: "B"},
			"B->C": {Source: "B", Target: "C"},
		}
		// iTheta is a colatitude-like offset (always >= 0 by construction: it comes from
		// angularDistance/acos, which never returns negative) — a negative iTheta would
		// alias with (-iTheta, iPhi+π/stepPhi) representing the identical direction, so
		// round-trip test data must stick to iTheta >= 0. Likewise iTheta == 0 (straight
		// continuation along the pole) makes iPhi (bearing) undefined/degenerate — atan2
		// resolves it to 0 regardless of the authored value — so round-trip test data
		// must keep iTheta != 0 wherever iPhi is meant to be recovered.
		want := map[string]quantizedOffset{
			"A": {iTheta: 2, iPhi: -1, iR: 1, parent: "R"},
			"B": {iTheta: 1, iPhi: 3, iR: 2, parent: "A"},
			"C": {iTheta: 1, iPhi: 2, iR: 1, parent: "B"},
		}
		parent, roots := buildSpanningTree(edges)
		full := map[string]quantizedOffset{"R": {parent: ""}}
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
		if g := got["R"]; g.parent != "" || g.iTheta != 0 || g.iPhi != 0 || g.iR != 0 {
			t.Fatalf("root offset = %+v, want zero offset with no parent", g)
		}
	})

	t.Run("branching node", func(t *testing.T) {
		edges := map[string]EdgeEndpoints{
			"P->X": {Source: "P", Target: "X"},
			"P->Y": {Source: "P", Target: "Y"},
			"P->Z": {Source: "P", Target: "Z"},
		}
		want := map[string]quantizedOffset{
			"X": {iTheta: 1, iPhi: 0, iR: 1, parent: "P"},
			"Y": {iTheta: 1, iPhi: 4, iR: 1, parent: "P"},
			"Z": {iTheta: 2, iPhi: -3, iR: 2, parent: "P"},
		}
		parent, roots := buildSpanningTree(edges)
		full := map[string]quantizedOffset{"P": {parent: ""}}
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
	edges := map[string]EdgeEndpoints{
		"R->A": {Source: "R", Target: "A"},
		"A->B": {Source: "A", Target: "B"},
	}
	anchor := vec3{X: 0, Y: 0, Z: 0}
	// Arbitrary (not grid-aligned) centers, reachable from anchor via R.
	centers := map[string]vec3{
		"R": anchor,
		"A": vec3{X: 3.1, Y: 0.4, Z: -2.2},
		"B": vec3{X: 5.9, Y: 1.7, Z: -3.8},
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
