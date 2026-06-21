package Wiring

import (
	"context"
	"testing"

	T "github.com/dtauraso/wirefold/Trace"
)

// Theta lock: node 6 shares node 2's θ (angle from node 1's +y up-pole) while
// keeping its own azimuth φ and radius. Center 1 at origin; surface nodes 2 and 6
// via edges 1→2, 1→6. Dragging node 2 should leave node 6 on the same latitude ring
// as node 2 (equal θ), unchanged in φ and r.
func buildThetaLockFixture() (*MoveDispatch, context.CancelFunc) {
	centers := map[string]vec3{
		"1": {0, 0, 0},
		"2": {10, 0, 5},  // θ=π/2, φ=atan2(5,10)
		"6": {10, 0, -5}, // θ=π/2, φ=atan2(-5,10) — own longitude
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
	md.addThetaLock("1", "2", "6")
	ctx, cancel := context.WithCancel(context.Background())
	md.Start(ctx)
	return md, cancel
}

func TestThetaLockEqualizesAngleFromPole(t *testing.T) {
	md, cancel := buildThetaLockFixture()
	defer cancel()
	const eps = 1e-6

	// Follower 6's own φ and r BEFORE the move — the lock must preserve these.
	before, _ := md.roots.surfaceCoord("1", "6")

	// Drag node 2 to a new spot, changing its θ (angle from the +y pole).
	md.RootMove("2", vec3{X: 8, Y: 4, Z: 7})

	p2, _ := md.roots.surfaceCoord("1", "2")
	p6, _ := md.roots.surfaceCoord("1", "6")

	// θ equalized: 6 now shares 2's angle from node 1's up-pole.
	if d := p6.Theta - p2.Theta; d < -eps || d > eps {
		t.Errorf("follower θ=%v != leader θ=%v (angle-from-pole not equalized)", p6.Theta, p2.Theta)
	}
	// 6 keeps its OWN azimuth φ (own longitude) — not mirrored, not copied from 2.
	if d := p6.Phi - before.Phi; d < -eps || d > eps {
		t.Errorf("follower φ changed: %v != %v (own longitude not preserved)", p6.Phi, before.Phi)
	}
	// Both end on node 1's sphere, so they share radius (the reach radius) by construction.
	if d := p6.R - p2.R; d < -eps || d > eps {
		t.Errorf("6 radius %v != 2 radius %v (not on the same sphere)", p6.R, p2.R)
	}
}
