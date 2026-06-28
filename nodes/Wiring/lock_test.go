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

// dist3 is the Euclidean distance between two world points.
func dist3(a, b vec3) float64 {
	dx, dy, dz := a.X-b.X, a.Y-b.Y, a.Z-b.Z
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

// meridianPerp is the off-meridian-plane normal: the φ=90° axis of the polar
// frame (pole = +y). The φ=0 meridian plane is the set of points whose component
// along this axis is zero, so the off-plane component of any edge is v·perp.
var meridianPerp = polar2cart(polar{R: 1, Theta: math.Pi / 2, Phi: math.Pi / 2})

// Pole-safety: the re-pin must NOT lurch even when the dragged node sits straight
// above the other (the old φ=atan2 re-pin was singular/snapping there). Node 6 at
// the origin, node 5 dragged straight above 6 with a small off-plane wobble. The
// re-pin drops only the off-plane component, so node 6 moves a SMALL amount and the
// resulting edge lies in the meridian plane (off-plane component ≈ 0).
func TestPhiZeroLockPoleSafe(t *testing.T) {
	centers := map[string]vec3{
		"6": {0, 0, 0},
		"5": {0, 10, 0.5}, // straight above 6 plus a small off-plane (z) wobble
	}
	md := &MoveDispatch{}
	md.roots = buildRoots(centers)
	md.addPhiZeroLock("6", "5")

	sixPre, _ := md.roots.world("6")
	fivePre, _ := md.roots.world("5")

	md.applyLocks("5") // drag node 5 (follower) → node 6 (other) is re-pinned

	// Dragged node 5 does not move.
	fivePost, _ := md.roots.world("5")
	if d := dist3(fivePre, fivePost); d > 1e-9 {
		t.Errorf("dragged node 5 moved by %v (want 0)", d)
	}

	// Node 6 moved only a small amount (the off-plane drop), NOT a half-circle lurch.
	sixPost, _ := md.roots.world("6")
	if d := dist3(sixPre, sixPost); d >= 1 {
		t.Errorf("node 6 lurched by %v (want small, < 1)", d)
	}

	// Resulting edge (5−6) lies in the meridian plane: off-plane component ≈ 0.
	edge := fivePost.sub(sixPost)
	if off := edge.dot(meridianPerp); off < -1e-9 || off > 1e-9 {
		t.Errorf("edge off-plane component %v (want ≈0)", off)
	}
}

// Symmetry: same setup but dragging node 6 instead re-pins node 5. The dragged node
// stays put, the other moves only a small off-plane drop, and the edge lies in the
// meridian plane. No 0-vs-π branch: dropping the off-plane component is symmetric.
func TestPhiZeroLockSymmetric(t *testing.T) {
	centers := map[string]vec3{
		"6": {0, 0, 0},
		"5": {0, 10, 0.5},
	}
	md := &MoveDispatch{}
	md.roots = buildRoots(centers)
	md.addPhiZeroLock("6", "5")

	sixPre, _ := md.roots.world("6")
	fivePre, _ := md.roots.world("5")

	md.applyLocks("6") // drag node 6 (center) → node 5 (other) is re-pinned

	// Dragged node 6 does not move.
	sixPost, _ := md.roots.world("6")
	if d := dist3(sixPre, sixPost); d > 1e-9 {
		t.Errorf("dragged node 6 moved by %v (want 0)", d)
	}

	// Node 5 moved only a small amount.
	fivePost, _ := md.roots.world("5")
	if d := dist3(fivePre, fivePost); d >= 1 {
		t.Errorf("node 5 lurched by %v (want small, < 1)", d)
	}

	// Edge lies in the meridian plane.
	edge := fivePost.sub(sixPost)
	if off := edge.dot(meridianPerp); off < -1e-9 || off > 1e-9 {
		t.Errorf("edge off-plane component %v (want ≈0)", off)
	}
}

// After the re-pin the edge lies in the φ=0 meridian plane: φ(5 about 6) ≈ 0. This
// is the load-time expectation (the edge is in-plane) expressed in polar terms.
func TestPhiZeroLockEdgeInMeridianPlane(t *testing.T) {
	centers := map[string]vec3{
		"6": {3, 2, 1},            // center, off-origin
		"5": {3 + 8, 2 + 4, 1 + 6}, // nonzero off-plane (z) component about node 6
	}
	md := &MoveDispatch{}
	md.roots = buildRoots(centers)
	md.addPhiZeroLock("6", "5")

	before, ok := md.roots.surfaceCoord("6", "5")
	if !ok {
		t.Fatal("surfaceCoord(6,5) not resolvable before lock")
	}
	if before.Phi > -1e-9 && before.Phi < 1e-9 {
		t.Fatalf("fixture invalid: φ should be clearly nonzero, got %v", before.Phi)
	}

	md.applyLocks("6") // drag center 6 → follower 5 projected onto the meridian plane

	after, ok := md.roots.surfaceCoord("6", "5")
	if !ok {
		t.Fatal("surfaceCoord(6,5) not resolvable after lock")
	}
	if after.Phi < -1e-6 || after.Phi > 1e-6 {
		t.Errorf("φ not in meridian plane: got %v (want ≈0)", after.Phi)
	}
}

// 7↔5 mirror of TestPhiZeroLockPoleSafe: the 7→5 edge is coupled identically to 6→5.
// Node 7 at the origin, node 5 dragged straight above 7 with a small off-plane wobble;
// the re-pin drops only the off-plane component, so node 7 moves a SMALL amount and the
// resulting edge lies in the meridian plane.
func TestPhiZeroLock75PoleSafe(t *testing.T) {
	centers := map[string]vec3{
		"7": {0, 0, 0},
		"5": {0, 10, 0.5}, // straight above 7 plus a small off-plane (z) wobble
	}
	md := &MoveDispatch{}
	md.roots = buildRoots(centers)
	md.addPhiZeroLock("7", "5")

	sevenPre, _ := md.roots.world("7")
	fivePre, _ := md.roots.world("5")

	md.applyLocks("5") // drag node 5 (follower) → node 7 (other) is re-pinned

	// Dragged node 5 does not move.
	fivePost, _ := md.roots.world("5")
	if d := dist3(fivePre, fivePost); d > 1e-9 {
		t.Errorf("dragged node 5 moved by %v (want 0)", d)
	}

	// Node 7 moved only a small amount (the off-plane drop), NOT a half-circle lurch.
	sevenPost, _ := md.roots.world("7")
	if d := dist3(sevenPre, sevenPost); d >= 1 {
		t.Errorf("node 7 lurched by %v (want small, < 1)", d)
	}

	// Resulting edge (5−7) lies in the meridian plane: off-plane component ≈ 0.
	edge := fivePost.sub(sevenPost)
	if off := edge.dot(meridianPerp); off < -1e-9 || off > 1e-9 {
		t.Errorf("edge off-plane component %v (want ≈0)", off)
	}
}

// 7↔5 mirror of TestPhiZeroLockSymmetric: dragging node 7 re-pins node 5.
func TestPhiZeroLock75Symmetric(t *testing.T) {
	centers := map[string]vec3{
		"7": {0, 0, 0},
		"5": {0, 10, 0.5},
	}
	md := &MoveDispatch{}
	md.roots = buildRoots(centers)
	md.addPhiZeroLock("7", "5")

	sevenPre, _ := md.roots.world("7")
	fivePre, _ := md.roots.world("5")

	md.applyLocks("7") // drag node 7 (center) → node 5 (other) is re-pinned

	// Dragged node 7 does not move.
	sevenPost, _ := md.roots.world("7")
	if d := dist3(sevenPre, sevenPost); d > 1e-9 {
		t.Errorf("dragged node 7 moved by %v (want 0)", d)
	}

	// Node 5 moved only a small amount.
	fivePost, _ := md.roots.world("5")
	if d := dist3(fivePre, fivePost); d >= 1 {
		t.Errorf("node 5 lurched by %v (want small, < 1)", d)
	}

	// Edge lies in the meridian plane.
	edge := fivePost.sub(sevenPost)
	if off := edge.dot(meridianPerp); off < -1e-9 || off > 1e-9 {
		t.Errorf("edge off-plane component %v (want ≈0)", off)
	}
}

// 7↔5 mirror of TestPhiZeroLockEdgeInMeridianPlane: after the re-pin the 7→5 edge
// lies in the φ=0 meridian plane.
func TestPhiZeroLock75EdgeInMeridianPlane(t *testing.T) {
	centers := map[string]vec3{
		"7": {3, 2, 1},             // center, off-origin
		"5": {3 + 8, 2 + 4, 1 + 6}, // nonzero off-plane (z) component about node 7
	}
	md := &MoveDispatch{}
	md.roots = buildRoots(centers)
	md.addPhiZeroLock("7", "5")

	before, ok := md.roots.surfaceCoord("7", "5")
	if !ok {
		t.Fatal("surfaceCoord(7,5) not resolvable before lock")
	}
	if before.Phi > -1e-9 && before.Phi < 1e-9 {
		t.Fatalf("fixture invalid: φ should be clearly nonzero, got %v", before.Phi)
	}

	md.applyLocks("7") // drag center 7 → follower 5 projected onto the meridian plane

	after, ok := md.roots.surfaceCoord("7", "5")
	if !ok {
		t.Fatal("surfaceCoord(7,5) not resolvable after lock")
	}
	if after.Phi < -1e-6 || after.Phi > 1e-6 {
		t.Errorf("φ not in meridian plane: got %v (want ≈0)", after.Phi)
	}
}
