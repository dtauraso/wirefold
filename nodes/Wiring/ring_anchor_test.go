// ring_anchor_test.go — unit tests for the flat ring-anchor geometry added in
// feat(geometry): port anchorId indexes a flat ring-anchor array.

package Wiring

import (
	"math"
	"testing"
)

// TestRingAnchorCount verifies N = floor(2*pi*R / (d+p)) with d=8, p=2.
func TestRingAnchorCount(t *testing.T) {
	pitch := ringAnchorDiameter + ringAnchorPadding // 10.0
	cases := []struct {
		R     float64
		wantN int
	}{
		{R: 20.0, wantN: int(2 * math.Pi * 20.0 / pitch)}, // ~12
		{R: 30.0, wantN: int(2 * math.Pi * 30.0 / pitch)}, // ~18
		{R: 0.1, wantN: 1}, // degenerate: minimum 1
	}
	for _, c := range cases {
		got := ringAnchorCount(c.R)
		if got != c.wantN {
			t.Errorf("ringAnchorCount(%.1f) = %d, want %d", c.R, got, c.wantN)
		}
	}
}

// TestRingAnchorDirUnitLength verifies every direction is a unit vector lying
// on the node's EQUATORIAL ring (theta=pi/2 → Y=0; polar-torus model).
func TestRingAnchorDirUnitLength(t *testing.T) {
	R := 25.0
	N := ringAnchorCount(R)
	for i := range N {
		d := ringAnchorDir(R, i)
		length := math.Sqrt(d.X*d.X + d.Y*d.Y + d.Z*d.Z)
		if math.Abs(length-1.0) > 1e-9 {
			t.Errorf("ringAnchorDir(%d) length = %g, want 1.0", i, length)
		}
		if math.Abs(d.Y) > 1e-9 {
			t.Errorf("ringAnchorDir(%d).Y = %g, want 0 (equatorial ring, theta=pi/2)", i, d.Y)
		}
	}
}

// TestRingAnchorDirAngle verifies anchor 0 is at azimuth 0 (pointing +X) and
// subsequent anchors are evenly spaced in phi around the equatorial (XZ) ring.
func TestRingAnchorDirAngle(t *testing.T) {
	R := 20.0
	N := ringAnchorCount(R)
	pitch := 2 * math.Pi / float64(N)

	for _, i := range []int{0, 1, N / 2} {
		d := ringAnchorDir(R, i)
		wantPhi := float64(i) * pitch
		wantX := math.Cos(wantPhi)
		wantZ := math.Sin(wantPhi)
		if math.Abs(d.X-wantX) > 1e-9 || math.Abs(d.Z-wantZ) > 1e-9 {
			t.Errorf("ringAnchorDir(%d) = {%.6f, %.6f}, want {%.6f, %.6f}",
				i, d.X, d.Z, wantX, wantZ)
		}
	}
}

// TestRingAnchorDirWraps verifies that index N wraps back to index 0.
func TestRingAnchorDirWraps(t *testing.T) {
	R := 20.0
	N := ringAnchorCount(R)
	d0 := ringAnchorDir(R, 0)
	dN := ringAnchorDir(R, N)
	if d0.X != dN.X || d0.Z != dN.Z {
		t.Errorf("ringAnchorDir(N) should wrap to ringAnchorDir(0); got %v vs %v", dN, d0)
	}
}

// TestPortDirAnchorIdPath verifies that a port with AnchorId set resolves via
// the ring path.
func TestPortDirAnchorIdPath(t *testing.T) {
	kind := "Splitter" // any kind; we just need a stable radius
	R := nodeRadius(kind)
	N := ringAnchorCount(R)
	anchorIdx := 1 % N // safe even if N==1

	g := nodeGeom{
		Kind: kind,
		Inputs: []portGeom{
			{Name: "ring_port", AnchorId: &anchorIdx},
		},
	}

	got, ok := portDir(g, "ring_port", true)
	if !ok {
		t.Fatal("portDir ring_port: not found")
	}
	want := ringAnchorDir(R, anchorIdx)
	if math.Abs(got.X-want.X) > 1e-9 || math.Abs(got.Y-want.Y) > 1e-9 || got.Z != want.Z {
		t.Errorf("portDir(ring_port) = %v, want %v", got, want)
	}
}
