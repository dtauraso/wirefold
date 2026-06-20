package Wiring

import (
	"math"
	"testing"
)

func TestPrismFromPoints(t *testing.T) {
	pts := []vec3{{1, 2, 3}, {-4, 5, -6}, {0, -1, 2}}
	p := prismFromPoints(pts)
	if p.Min != (vec3{-4, -1, -6}) {
		t.Errorf("min = %v want (-4,-1,-6)", p.Min)
	}
	if p.Max != (vec3{1, 5, 3}) {
		t.Errorf("max = %v want (1,5,3)", p.Max)
	}
	if c := p.center(); c != (vec3{-1.5, 2, -1.5}) {
		t.Errorf("center = %v want (-1.5,2,-1.5)", c)
	}
}

func TestPrismContains(t *testing.T) {
	p := prism{Min: vec3{0, 0, 0}, Max: vec3{10, 10, 10}}
	if !p.contains(vec3{5, 5, 5}) {
		t.Error("should contain interior point")
	}
	if p.contains(vec3{-1, 5, 5}) {
		t.Error("should not contain point outside")
	}
}

func TestLargeSphereRadius(t *testing.T) {
	const eps = 1e-9
	center := vec3{0, 0, 0}
	pts := []vec3{{3, 0, 0}, {0, 4, 0}, {0, 0, 5}}
	if r := largeSphereRadius(center, pts); math.Abs(r-5) > eps {
		t.Errorf("radius = %v want 5", r)
	}
}

// buildRoots: every node's world position is recovered from its root within epsilon.
func TestBuildRootsRecoversWorld(t *testing.T) {
	const eps = 1e-9
	centers := map[string]vec3{
		"1": {0, 0, 0},
		"2": {100, -6, 6},
		"6": {90, -1, -47},
		"8": {-30, 20, 10},
	}
	rs := buildRoots(centers)
	for id, want := range centers {
		got, ok := rs.world(id)
		if !ok {
			t.Fatalf("no world for %s", id)
		}
		if got.sub(want).length() > eps {
			t.Errorf("node %s world = %v want %v", id, got, want)
		}
	}
	// Origin is the bounding-box center; radius circumscribes all nodes.
	if rs.origin != rs.prism.center() {
		t.Errorf("origin %v != prism center %v", rs.origin, rs.prism.center())
	}
	for id, want := range centers {
		if d := want.sub(rs.origin).length(); d > rs.radius+eps {
			t.Errorf("node %s at %v is outside large sphere radius %v", id, want, rs.radius)
		}
	}
}

// A node's world position survives cartesian→root→cartesian about a prism center.
func TestRootRoundTrip(t *testing.T) {
	const eps = 1e-9
	origin := vec3{10, -5, 2}
	pos := vec3{13, 1, -4}
	got := worldFromRoot(rootFromCartesian(pos, origin), origin)
	if math.Abs(got.X-pos.X) > eps || math.Abs(got.Y-pos.Y) > eps || math.Abs(got.Z-pos.Z) > eps {
		t.Errorf("root round-trip %v -> %v", pos, got)
	}
}

// reOrigin preserves every node's world position within epsilon.
func TestReOriginPreservesWorld(t *testing.T) {
	const eps = 1e-9
	centers := map[string]vec3{
		"a": {0, 0, 0},
		"b": {100, -6, 6},
		"c": {90, -1, -47},
		"d": {-30, 20, 10},
	}
	rs := buildRoots(centers)
	newOrigin := vec3{X: 123.4, Y: -7.8, Z: 9.0}
	rs.reOrigin(newOrigin)
	for id, want := range centers {
		got, ok := rs.world(id)
		if !ok {
			t.Fatalf("no world for %s after reOrigin", id)
		}
		if got.sub(want).length() > eps {
			t.Errorf("node %s world = %v want %v after reOrigin", id, got, want)
		}
	}
	if rs.origin != newOrigin {
		t.Errorf("origin not updated: got %v want %v", rs.origin, newOrigin)
	}
}
