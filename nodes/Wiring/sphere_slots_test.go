package Wiring

import (
	"math"
	"testing"
)

func TestProjectToSphere(t *testing.T) {
	center := vec3{X: 1, Y: 2, Z: 3}
	R := 5.0
	// Neighbor off along +X; projection must be at distance R along that dir.
	neighbor := vec3{X: 11, Y: 2, Z: 3}
	got := projectToSphere(center, R, neighbor)

	dx := got.X - center.X
	dy := got.Y - center.Y
	dz := got.Z - center.Z
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if math.Abs(dist-R) > 1e-9 {
		t.Fatalf("projected point at distance %v, want %v", dist, R)
	}
	// Direction preserved: should land at center + R*(+X).
	want := vec3{X: center.X + R, Y: center.Y, Z: center.Z}
	if math.Abs(got.X-want.X) > 1e-9 || math.Abs(got.Y-want.Y) > 1e-9 || math.Abs(got.Z-want.Z) > 1e-9 {
		t.Fatalf("projected point %+v, want %+v", got, want)
	}
}

func TestProjectToSphereDegenerate(t *testing.T) {
	center := vec3{X: 4, Y: 5, Z: 6}
	R := 2.0
	// neighbor == center: zero-length direction is handled (still on surface).
	got := projectToSphere(center, R, center)
	dx := got.X - center.X
	dy := got.Y - center.Y
	dz := got.Z - center.Z
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if math.Abs(dist-R) > 1e-9 {
		t.Fatalf("degenerate projection at distance %v, want %v (on surface)", dist, R)
	}
}

func TestDiameterStepAngle(t *testing.T) {
	R := 10.0
	// angle = diameter / R
	if got := diameterStepAngle(R, 2.0); math.Abs(got-0.2) > 1e-12 {
		t.Fatalf("diameterStepAngle(10,2)=%v, want 0.2", got)
	}
	// Smaller diameter -> smaller (finer) step.
	small := diameterStepAngle(R, 1.0)
	large := diameterStepAngle(R, 4.0)
	if !(small < large) {
		t.Fatalf("expected smaller diameter to give finer step: small=%v large=%v", small, large)
	}
	// Guard R <= 0.
	if got := diameterStepAngle(0, 3.0); got != 0 {
		t.Fatalf("diameterStepAngle(0,3)=%v, want 0", got)
	}
}
