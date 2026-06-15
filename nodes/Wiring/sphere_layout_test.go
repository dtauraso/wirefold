package Wiring

import (
	"math"
	"testing"
)

func r(v float64) *float64       { return &v }
func d(x, y, z float64) *[3]float64 { return &[3]float64{x, y, z} }

func approxVec(a, b vec3, eps float64) bool {
	return math.Abs(a.X-b.X) < eps && math.Abs(a.Y-b.Y) < eps && math.Abs(a.Z-b.Z) < eps
}

// Anchor + 2 children with known dirs: each child placed at anchor + R_anchor*dir.
func TestSphereChainPlacesChildrenAtParentRDir(t *testing.T) {
	const R = 100.0
	nodes := map[string]nodeGeom{
		"1": {Kind: "X", R: r(R)},
		"2": {Kind: "X", R: r(R), Dir: d(1, 0, 0)},
		"3": {Kind: "X", R: r(R), Dir: d(0, 1, 0)},
	}
	edges := []sphereEdge{{Source: "1", Target: "2"}, {Source: "1", Target: "3"}}

	pos := computeSphereChainPositions(nodes, edges)
	if pos == nil {
		t.Fatal("expected non-nil positions (R is set)")
	}
	if !approxVec(pos["1"], vec3{0, 0, 0}, 1e-9) {
		t.Errorf("anchor not at origin: %+v", pos["1"])
	}
	if !approxVec(pos["2"], vec3{R, 0, 0}, 1e-9) {
		t.Errorf("child 2 want {%g,0,0}, got %+v", R, pos["2"])
	}
	if !approxVec(pos["3"], vec3{0, R, 0}, 1e-9) {
		t.Errorf("child 3 want {0,%g,0}, got %+v", R, pos["3"])
	}
}

// A back edge (cycle) must not re-place a node nor loop forever.
func TestSphereChainCycleTerminates(t *testing.T) {
	const R = 50.0
	nodes := map[string]nodeGeom{
		"1": {R: r(R)},
		"2": {R: r(R), Dir: d(1, 0, 0)},
		"3": {R: r(R), Dir: d(0, 1, 0)},
	}
	// 1-2, 2-3, 3-1 → cycle. 3's back edge to 1 must be ignored.
	edges := []sphereEdge{
		{Source: "1", Target: "2"},
		{Source: "2", Target: "3"},
		{Source: "3", Target: "1"},
	}
	pos := computeSphereChainPositions(nodes, edges)
	if len(pos) != 3 {
		t.Fatalf("want 3 placed nodes, got %d", len(pos))
	}
	if !approxVec(pos["1"], vec3{0, 0, 0}, 1e-9) {
		t.Errorf("anchor moved by back edge: %+v", pos["1"])
	}
	// BFS reaches both 2 and 3 directly from anchor 1 (1-2 and the 3-1 edge):
	// 2 placed from 1 → {R,0,0}; 3 placed from 1 → {0,R,0}. The 2-3 edge is a
	// cross edge to an already-placed node and is ignored (terminates the cycle).
	if !approxVec(pos["2"], vec3{R, 0, 0}, 1e-9) {
		t.Errorf("node 2: %+v", pos["2"])
	}
	if !approxVec(pos["3"], vec3{0, R, 0}, 1e-9) {
		t.Errorf("node 3: %+v", pos["3"])
	}
}

// No node carries R → lattice mode; returns nil.
func TestSphereChainNoRReturnsNil(t *testing.T) {
	nodes := map[string]nodeGeom{
		"1": {Kind: "X"},
		"2": {Kind: "X"},
	}
	edges := []sphereEdge{{Source: "1", Target: "2"}}
	if pos := computeSphereChainPositions(nodes, edges); pos != nil {
		t.Errorf("expected nil (no R set), got %+v", pos)
	}
}

// A reached node with nil Dir falls back to +Y placement (not skipped).
func TestSphereChainNilDirFallsBackToY(t *testing.T) {
	const R = 10.0
	nodes := map[string]nodeGeom{
		"1": {R: r(R)},
		"2": {R: r(R)}, // nil Dir
	}
	edges := []sphereEdge{{Source: "1", Target: "2"}}
	pos := computeSphereChainPositions(nodes, edges)
	if !approxVec(pos["2"], vec3{0, R, 0}, 1e-9) {
		t.Errorf("nil-dir child want {0,%g,0}, got %+v", R, pos["2"])
	}
}

// Disconnected node is left out of the map (keeps its lattice pos).
func TestSphereChainDisconnectedExcluded(t *testing.T) {
	const R = 10.0
	nodes := map[string]nodeGeom{
		"1": {R: r(R)},
		"2": {R: r(R), Dir: d(1, 0, 0)},
		"9": {R: r(R), Dir: d(0, 1, 0)}, // unreachable from anchor "1"
	}
	edges := []sphereEdge{{Source: "1", Target: "2"}}
	pos := computeSphereChainPositions(nodes, edges)
	if _, ok := pos["9"]; ok {
		t.Errorf("disconnected node 9 should not be placed, got %+v", pos["9"])
	}
}
