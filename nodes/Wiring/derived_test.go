package Wiring

import (
	"math"
	"testing"
)

func testRootSet() rootSet {
	return buildRoots(map[string]vec3{
		"1": {0, 0, 0},
		"2": {100, 0, 0},  // distance 100 from center 1
		"6": {0, 0, 60},   // distance 60 from center 1
		"5": {0, 150, 0},  // surface of 6 only
	})
}

func TestSphereR_ReachToFarthest(t *testing.T) {
	const eps = 1e-6
	rs := testRootSet()
	edges := []sphereEdge{{"1", "2"}, {"1", "6"}}
	// R for center 1 = farthest surface node = node 2 at distance 100.
	if r := rs.sphereR("1", edges); math.Abs(r-100) > eps {
		t.Errorf("sphereR(1) = %v want 100", r)
	}
	// A center with no surface nodes → 0.
	if r := rs.sphereR("2", edges); r != 0 {
		t.Errorf("sphereR(2) = %v want 0", r)
	}
}

func TestSurfaceCoordRecoversWorld(t *testing.T) {
	const eps = 1e-6
	rs := testRootSet()
	// Surface coord of node 2 relative to center 1, converted back, equals
	// node 2's world position.
	p, ok := rs.surfaceCoord("1", "2")
	if !ok {
		t.Fatal("no surface coord")
	}
	c1, _ := rs.world("1")
	got := polar2cart(p).add(c1)
	w2, _ := rs.world("2")
	if got.sub(w2).length() > eps {
		t.Errorf("surface coord -> %v want %v", got, w2)
	}
}

func TestRingNormals(t *testing.T) {
	if verticalRingNormal != (vec3{0, 0, 1}) {
		t.Errorf("vertical ring normal = %v want +z (chord-lock disk)", verticalRingNormal)
	}
	if flatRingNormal != (vec3{0, 1, 0}) {
		t.Errorf("flat ring normal = %v want +y", flatRingNormal)
	}
}
