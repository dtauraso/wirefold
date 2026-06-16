package Wiring

import (
	"context"
	"testing"

	T "github.com/dtauraso/wirefold/Trace"
)

// Chord lock: node 6 mirrors node 2 across node 1's vertical (φ=0) disk.
// Center 1 at origin; surface nodes 2 and 6 via edges 1→2, 1→6. Dragging node 2
// should set node 6 = mirror_φ(node 2): same r, same θ (same y, same x), z negated.
func buildChordLockFixture() (*MoveDispatch, context.CancelFunc) {
	centers := map[string]vec3{
		"1": {0, 0, 0},
		"2": {10, 0, 5},
		"6": {10, 0, -5}, // initial mirror of 2 across the φ=0 (z=0) disk
	}
	geoms := map[string]nodeGeom{}
	for id, c := range centers {
		cc := c
		geoms[id] = nodeGeom{Kind: "FanInSrc", Center: &cc}
	}
	edges := map[string]EdgeEndpoints{
		"e12": {Source: "1", Target: "2", SourceHandle: "Out", TargetHandle: "In"},
		"e16": {Source: "1", Target: "6", SourceHandle: "Out", TargetHandle: "In"},
	}
	tr := T.New(256)
	md := newMoveDispatch(geoms, edges, tr)
	md.setRoots(buildRoots(centers))
	md.addChordLock("1", "2", "6")
	ctx, cancel := context.WithCancel(context.Background())
	md.Start(ctx)
	return md, cancel
}

func TestChordLockMirrorsFollower(t *testing.T) {
	md, cancel := buildChordLockFixture()
	defer cancel()
	const eps = 1e-6

	// Drag node 2 to a new spot (different x, y, z).
	md.RootMove("2", vec3{X: 8, Y: 4, Z: 7})

	w2, _ := md.roots.world("2")
	w6, _ := md.roots.world("6")
	c1, _ := md.roots.world("1")

	// Center 1 at origin → mirror across z=0: 6 = (2.x, 2.y, -2.z).
	want := vec3{X: w2.X, Y: w2.Y, Z: 2*c1.Z - w2.Z}
	if w6.sub(want).length() > eps {
		t.Errorf("follower 6 = %v want %v (mirror of 2=%v across disk)", w6, want, w2)
	}
	// Both stay on node 1's sphere (equal distance from center).
	d2 := w2.sub(c1).length()
	d6 := w6.sub(c1).length()
	if d6 < d2-eps || d6 > d2+eps {
		t.Errorf("6 distance %v != 2 distance %v from center", d6, d2)
	}
}
