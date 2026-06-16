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
