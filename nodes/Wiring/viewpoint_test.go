package Wiring

import (
	"math"
	"math/rand"
	"testing"
)

// eye is a test-only oracle for the camera world position: pivot + r along pos.
// (Production Go never does this polar→Cartesian step; the renderer does, at its edge.)
func (v viewpoint) eye() vec3 {
	return v.pivot.add(polar2cart(polar{R: v.r, Theta: v.pos.Theta, Phi: v.pos.Phi}))
}

func randViewpoint(rng *rand.Rand) viewpoint {
	return viewpoint{
		pivot: vec3{X: rng.Float64() * 100, Y: rng.Float64() * 100, Z: rng.Float64() * 100},
		r:     10 + rng.Float64()*200,
		pos:   randDir(rng),
		up:    randDir(rng),
	}
}

func TestViewpointRotateIsRigid(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	const tol = 1e-7
	for i := 0; i < 3000; i++ {
		v := randViewpoint(rng)
		sep0 := angularDistance(v.pos, v.up) // frame "shape"
		r0, pivot0 := v.r, v.pivot
		v.rotate(rot{Axis: randDir(rng), Angle: (rng.Float64()*2 - 1) * math.Pi})
		// A rotation is rigid: pos↔up separation, radius, and pivot are all unchanged.
		if d := math.Abs(angularDistance(v.pos, v.up) - sep0); d > tol {
			t.Fatalf("rotate changed pos↔up separation by %v", d)
		}
		if v.r != r0 {
			t.Fatalf("rotate changed radius: %v != %v", v.r, r0)
		}
		if v.pivot != pivot0 {
			t.Fatalf("rotate moved pivot: %v != %v", v.pivot, pivot0)
		}
	}
}

func TestViewpointOrbitGrabFollows(t *testing.T) {
	rng := rand.New(rand.NewSource(12))
	const tol = 1e-7
	for i := 0; i < 3000; i++ {
		v := randViewpoint(rng)
		from := v.pos // grab the current position direction
		to := randDir(rng)
		v.orbit(from, to)
		// The grabbed direction lands on the target.
		if d := angularDistance(v.pos, to); d > tol {
			t.Fatalf("orbit: grabbed dir landed at %v, want %v (Δ=%v)", v.pos, to, d)
		}
	}
}

func TestViewpointZoomClamps(t *testing.T) {
	v := viewpoint{r: 100}
	v.zoom(0.5)
	if math.Abs(v.r-50) > 1e-12 {
		t.Fatalf("zoom 0.5 → %v want 50", v.r)
	}
	for i := 0; i < 100; i++ {
		v.zoom(0.5) // drive far below the floor
	}
	if v.r != viewpointMinDist {
		t.Fatalf("zoom floor: r=%v want %v", v.r, viewpointMinDist)
	}
	v.zoom(3)
	if math.Abs(v.r-3*viewpointMinDist) > 1e-12 {
		t.Fatalf("zoom out from floor → %v want %v", v.r, 3*viewpointMinDist)
	}
}

func TestViewpointPanMovesPivotAndEye(t *testing.T) {
	v := viewpoint{pivot: vec3{1, 2, 3}, r: 50, pos: dir{Theta: 1, Phi: 0.5}, up: dir{Theta: 0, Phi: 0}}
	eye0 := v.eye()
	delta := vec3{10, -5, 2}
	v.pan(delta)
	if v.pivot != (vec3{11, -3, 5}) {
		t.Fatalf("pan pivot = %v want {11 -3 5}", v.pivot)
	}
	// Camera rides with the pivot: eye shifts by the same delta.
	if got := v.eye().sub(eye0); got.sub(delta).length() > 1e-9 {
		t.Fatalf("pan eye shift = %v want %v", got, delta)
	}
}
