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

// TestPanSceneTranslatesRigidly verifies a scene pan moves every node's world by the same
// displacement (ScenePolar unchanged), the whole scene translating under a fixed camera.
func TestPanSceneTranslatesRigidly(t *testing.T) {
	root := writeTree(t)
	md := loadTreeMD(t, root)
	before := md.heldCenters()
	polarsBefore := md.heldPolar()

	disp := vec3{X: 12, Y: -5, Z: 7}
	md.PanScene(disp)

	after := md.heldCenters()
	polarsAfter := md.heldPolar()
	for id, b := range before {
		a := after[id]
		want := b.add(disp)
		if math.Abs(a.X-want.X) > 1e-9 || math.Abs(a.Y-want.Y) > 1e-9 || math.Abs(a.Z-want.Z) > 1e-9 {
			t.Fatalf("node %s world=%+v want %+v (rigid translate by %+v)", id, a, want, disp)
		}
		if polarsAfter[id] != polarsBefore[id] {
			t.Fatalf("node %s ScenePolar changed on pan: %+v -> %+v", id, polarsBefore[id], polarsAfter[id])
		}
	}
}
