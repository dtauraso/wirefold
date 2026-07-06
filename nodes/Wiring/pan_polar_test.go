package Wiring

import (
	"math"
	"testing"
)

// TestPanDisplacementPolarMatchesPlaneSlide locks the polar pan displacement to the known-
// correct cartesian planeSlide (camera right/up basis). The polar construction derives the
// lateral direction from the camera (θ,φ) + up via spherical trig, with r the magnitude —
// it must produce the same world displacement planeSlide does for the same drag.
func TestPanDisplacementPolarMatchesPlaneSlide(t *testing.T) {
	cases := []struct {
		pos, up dir
		dx, dy  float64
	}{
		{dir{Theta: 1.2, Phi: 0.3}, dir{Theta: 0.2, Phi: 1.1}, 10, 0},
		{dir{Theta: 1.2, Phi: 0.3}, dir{Theta: 0.2, Phi: 1.1}, 0, 12},
		{dir{Theta: 1.2, Phi: 0.3}, dir{Theta: 0.2, Phi: 1.1}, -7, 5},
		{dir{Theta: 2.0, Phi: -1.4}, dir{Theta: 1.0, Phi: 2.6}, 4, -9},
		{dir{Theta: 0.5, Phi: 2.2}, dir{Theta: 1.5, Phi: -0.7}, -6, -3},
	}
	const wpp = 0.37
	for _, c := range cases {
		got := panDisplacementPolar(c.pos, c.up, c.dx, c.dy, wpp)
		r, angle := deltaToPolar(c.dx, -c.dy)
		want := planeSlide(basisFromViewpoint(c.pos, c.up), r, angle, wpp)
		if math.Abs(got.X-want.X) > 1e-9 || math.Abs(got.Y-want.Y) > 1e-9 || math.Abs(got.Z-want.Z) > 1e-9 {
			t.Fatalf("pos=%v up=%v d=(%v,%v): polar=%+v want planeSlide=%+v", c.pos, c.up, c.dx, c.dy, got, want)
		}
	}
}
