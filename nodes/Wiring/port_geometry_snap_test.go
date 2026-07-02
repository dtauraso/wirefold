package Wiring

import (
	"testing"
)

// port_geometry_snap_test.go — snapToRingAnchorIndex picks the ring-anchor slot whose
// direction best matches a given vector. Expected indices are DERIVED from the code's own
// ringAnchorDir: feeding anchor i's exact direction back in must snap to i. Also covers the
// zero-vector guard (→ 0) and a between-anchors direction (snaps to the nearer slot).

func TestSnapToRingAnchorIndexRoundTrips(t *testing.T) {
	kind := "Hold" // 60x60 → nodeRadius 15 → ringAnchorCount 9 slots
	R := nodeRadius(kind)
	N := ringAnchorCount(R)
	if N < 2 {
		t.Fatalf("test needs a multi-anchor ring, got N=%d", N)
	}
	for i := 0; i < N; i++ {
		d := ringAnchorDir(R, i)
		if got := snapToRingAnchorIndex(kind, d); got != i {
			t.Fatalf("anchor %d direction snapped to %d (N=%d)", i, got, N)
		}
	}
}

// TestSnapToRingAnchorIndexZeroVector: the zero vector returns the safe default 0.
func TestSnapToRingAnchorIndexZeroVector(t *testing.T) {
	if got := snapToRingAnchorIndex("Hold", vec3{}); got != 0 {
		t.Fatalf("zero-vector snap: got %d, want 0", got)
	}
}

// TestSnapToRingAnchorIndexBetweenAnchors: a direction placed slightly past anchor 0
// toward anchor 1 still snaps to the nearest of the two (0 when biased toward 0).
func TestSnapToRingAnchorIndexNearest(t *testing.T) {
	kind := "Hold"
	R := nodeRadius(kind)
	d0 := ringAnchorDir(R, 0)
	d1 := ringAnchorDir(R, 1)
	// A point 90% toward anchor 0 from the midpoint of 0 and 1.
	mid := vec3{X: d0.X*0.9 + d1.X*0.1, Y: d0.Y*0.9 + d1.Y*0.1, Z: 0}
	if got := snapToRingAnchorIndex(kind, mid); got != 0 {
		t.Fatalf("direction biased toward anchor 0 snapped to %d, want 0", got)
	}
}
