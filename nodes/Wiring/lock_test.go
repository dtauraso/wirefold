package Wiring

import (
	"context"
	"math"
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

// φ=0 meridian lock: node 5 is pinned onto node 6's φ=0 (+x) meridian about node 6.
// Node 5 starts at a clearly nonzero φ; after the lock fires it keeps its distance R
// and latitude θ from node 6, with only its azimuth φ zeroed.
func TestPhiZeroLockMovesFollowerToMeridian(t *testing.T) {
	const eps = 1e-9
	centers := map[string]vec3{
		"6": {3, 2, 1},   // center, off-origin
		"5": {3 + 8, 2 + 4, 1 + 6}, // offset has nonzero φ about node 6 (z and x both nonzero)
	}
	md := &MoveDispatch{}
	md.roots = buildRoots(centers)
	md.addPhiZeroLock("6", "5")

	before, ok := md.roots.surfaceCoord("6", "5")
	if !ok {
		t.Fatal("surfaceCoord(6,5) not resolvable before lock")
	}
	if before.Phi > -eps && before.Phi < eps {
		t.Fatalf("fixture invalid: φ should be clearly nonzero, got %v", before.Phi)
	}

	md.applyLocks("6")

	after, ok := md.roots.surfaceCoord("6", "5")
	if !ok {
		t.Fatal("surfaceCoord(6,5) not resolvable after lock")
	}
	if after.Phi < -1e-6 || after.Phi > 1e-6 {
		t.Errorf("φ not zeroed: got %v (want ≈0)", after.Phi)
	}
	if d := after.R - before.R; d < -1e-6 || d > 1e-6 {
		t.Errorf("R changed: %v != %v (distance not preserved)", after.R, before.R)
	}
	if d := after.Theta - before.Theta; d < -1e-6 || d > 1e-6 {
		t.Errorf("θ changed: %v != %v (latitude not preserved)", after.Theta, before.Theta)
	}
}

// dist3 is the Euclidean distance between two world points.
func dist3(a, b vec3) float64 {
	dx, dy, dz := a.X-b.X, a.Y-b.Y, a.Z-b.Z
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

// Symmetric phi lock, drag the FOLLOWER (node 5): the dragged node stays put and
// the OTHER node (Center 6) is re-pinned onto node 5's meridian with φ=π. Mirrors
// the hand example: 6 near origin, 5 offset in +x with a small z perturbation.
// The π target must keep node 6's move SMALL (edge in the meridian plane), whereas
// the φ=0 target would fling node 6 a half-circle around the pole.
func TestPhiZeroLockDragFollowerStable(t *testing.T) {
	preDrag6 := vec3{0, 0, 0}
	// "after drag" state: 5 dragged to (12,6,1); 6 still at its pre-drag origin.
	centers := map[string]vec3{
		"6": preDrag6,
		"5": {12, 6, 1},
	}
	md := &MoveDispatch{}
	md.roots = buildRoots(centers)
	md.addPhiZeroLock("6", "5")

	// Pre-drag world position of node 5 (must be unchanged by the lock).
	fivePre, _ := md.roots.world("5")

	// Compute both candidate re-pins of node 6 about node 5 (φ=π is what the lock
	// must pick; φ=0 is the buggy half-circle target).
	p, ok := md.roots.surfaceCoord("5", "6")
	if !ok {
		t.Fatal("surfaceCoord(5,6) not resolvable")
	}
	fiveW, _ := md.roots.world("5")
	sixPi := fiveW.add(polar2cart(polar{R: p.R, Theta: p.Theta, Phi: math.Pi}))
	sixZero := fiveW.add(polar2cart(polar{R: p.R, Theta: p.Theta, Phi: 0}))

	md.applyLocks("5")

	// Node 5 (the dragged node) must not move.
	fivePost, _ := md.roots.world("5")
	if d := dist3(fivePre, fivePost); d > 1e-9 {
		t.Errorf("dragged node 5 moved by %v (want 0)", d)
	}

	// Node 6 re-pinned to the π target.
	sixPost, _ := md.roots.world("6")
	if d := dist3(sixPost, sixPi); d > 1e-6 {
		t.Errorf("node 6 not at π target: got %v want %v", sixPost, sixPi)
	}

	// Stability: π-target move must be MUCH smaller than the φ=0-target move.
	movePi := dist3(sixPost, preDrag6)
	moveZero := dist3(sixZero, preDrag6)
	if movePi >= moveZero {
		t.Errorf("π-target move %v not smaller than φ=0-target move %v", movePi, moveZero)
	}
	if movePi > 2 {
		t.Errorf("π-target move %v too large for a small drag (expected ≈1)", movePi)
	}
	if moveZero < 10 {
		t.Errorf("fixture invalid: φ=0-target move %v should be a large half-circle", moveZero)
	}
	t.Logf("node 6 move: π-target=%.4f φ=0-target=%.4f", movePi, moveZero)

	// Edge now lies in the meridian plane: φ(5 about 6) ≈ 0.
	after, ok := md.roots.surfaceCoord("6", "5")
	if !ok {
		t.Fatal("surfaceCoord(6,5) not resolvable after lock")
	}
	if after.Phi < -1e-6 || after.Phi > 1e-6 {
		t.Errorf("edge off-plane: φ(5 about 6)=%v (want ≈0)", after.Phi)
	}
}
