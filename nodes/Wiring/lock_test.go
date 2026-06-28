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

// moveFollower(6,5): dragging node 5 perpendicular to the meridian CARRIES node 6
// onto node 5's new meridian (5 moves perpendicular like 6 does; the perpendicular
// component is NOT dropped). Node 5 keeps its full dragged position; node 6 adopts
// node 5's perpendicular coordinate, so the edge ends in the (shared) meridian plane.
func TestPhiZeroFollowerLockDrag5CarriesSix(t *testing.T) {
	centers := map[string]vec3{
		"6": {0, 0, 0},
		"5": {0, 10, 0.5}, // straight above 6 plus a small off-plane (z) component
	}
	md := &MoveDispatch{}
	md.roots = buildRoots(centers)
	md.addPhiZeroFollowerLock("6", "5")

	fivePre, _ := md.roots.world("5")

	md.applyLocks("5", true) // drag node 5 perpendicular → node 6 carried onto 5's meridian

	// Node 5 keeps its full dragged position (its perpendicular component is NOT dropped).
	fivePost, _ := md.roots.world("5")
	if d := dist3(fivePre, fivePost); d > 1e-9 {
		t.Errorf("dragged node 5 moved by %v (want 0 — full position kept)", d)
	}

	// Node 6 adopts node 5's perpendicular coordinate (came onto 5's meridian).
	sixPost, _ := md.roots.world("6")
	if d := sixPost.dot(meridianPerp) - fivePost.dot(meridianPerp); d < -1e-9 || d > 1e-9 {
		t.Errorf("node 6 perp=%v != node 5 perp=%v (did not come onto 5's meridian)",
			sixPost.dot(meridianPerp), fivePost.dot(meridianPerp))
	}

	// Resulting edge (5−6) lies in the (shared) meridian plane: off-plane component ≈ 0.
	edge := fivePost.sub(sixPost)
	if off := edge.dot(meridianPerp); off < -1e-9 || off > 1e-9 {
		t.Errorf("edge off-plane component %v (want ≈0)", off)
	}
}

// moveFollower(6,5): dragging node 6 (the anchor) re-pins the FOLLOWER node 5. The
// dragged anchor stays put (it is the move source), node 5 moves only a small
// off-plane drop, and the edge lies in the meridian plane.
func TestPhiZeroFollowerLockDrag6MovesFive(t *testing.T) {
	centers := map[string]vec3{
		"6": {0, 0, 0},
		"5": {0, 10, 0.5},
	}
	md := &MoveDispatch{}
	md.roots = buildRoots(centers)
	md.addPhiZeroFollowerLock("6", "5")

	sixPre, _ := md.roots.world("6")
	fivePre, _ := md.roots.world("5")

	md.applyLocks("6", true) // drag node 6 (anchor) → node 5 (follower) re-pinned

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
	md.addPhiZeroFollowerLock("6", "5")

	before, ok := md.roots.surfaceCoord("6", "5")
	if !ok {
		t.Fatal("surfaceCoord(6,5) not resolvable before lock")
	}
	if before.Phi > -1e-9 && before.Phi < 1e-9 {
		t.Fatalf("fixture invalid: φ should be clearly nonzero, got %v", before.Phi)
	}

	md.applyLocks("6", true) // drag center 6 → follower 5 projected onto the meridian plane

	after, ok := md.roots.surfaceCoord("6", "5")
	if !ok {
		t.Fatal("surfaceCoord(6,5) not resolvable after lock")
	}
	if after.Phi < -1e-6 || after.Phi > 1e-6 {
		t.Errorf("φ not in meridian plane: got %v (want ≈0)", after.Phi)
	}
}

// moveCenter(7,5): node 5 DRAGS node 7. Dragging node 5 moves the CENTER node 7 to
// keep node 5 on node 7's meridian. Node 7 at the origin, node 5 above with a small
// off-plane wobble; the projection drops only the off-plane component, so node 7
// moves a SMALL amount and the edge lies in the meridian plane.
func TestPhiZeroCenterLockDrag5MovesSeven(t *testing.T) {
	centers := map[string]vec3{
		"7": {0, 0, 0},
		"5": {0, 10, 0.5}, // straight above 7 plus a small off-plane (z) wobble
	}
	md := &MoveDispatch{}
	md.roots = buildRoots(centers)
	md.addPhiZeroCenterLock("7", "5")

	sevenPre, _ := md.roots.world("7")
	fivePre, _ := md.roots.world("5")

	md.applyLocks("5", true) // drag node 5 → node 7 (center) follows

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

// moveCenter(7,5): dragging node 7 perpendicular to the meridian CARRIES node 5 onto
// node 7's new meridian (7 moves perpendicular like 6 does; the perpendicular
// component is NOT dropped). Node 7 keeps its full dragged position; node 5 adopts
// node 7's perpendicular coordinate, so the edge ends in the (shared) meridian plane.
func TestPhiZeroCenterLockDrag7CarriesFive(t *testing.T) {
	centers := map[string]vec3{
		"7": {0, 0, 0.5}, // dragged with a small off-plane (z) component
		"5": {0, 10, 0},
	}
	md := &MoveDispatch{}
	md.roots = buildRoots(centers)
	md.addPhiZeroCenterLock("7", "5")

	sevenPre, _ := md.roots.world("7")

	md.applyLocks("7", true) // drag node 7 perpendicular → node 5 carried onto 7's meridian

	// Node 7 keeps its full dragged position (its perpendicular component is NOT dropped).
	sevenPost, _ := md.roots.world("7")
	if d := dist3(sevenPre, sevenPost); d > 1e-9 {
		t.Errorf("dragged node 7 moved by %v (want 0 — full position kept)", d)
	}

	// Node 5 adopts node 7's perpendicular coordinate (came onto 7's meridian).
	fivePost, _ := md.roots.world("5")
	if d := fivePost.dot(meridianPerp) - sevenPost.dot(meridianPerp); d < -1e-9 || d > 1e-9 {
		t.Errorf("node 5 perp=%v != node 7 perp=%v (did not come onto 7's meridian)",
			fivePost.dot(meridianPerp), sevenPost.dot(meridianPerp))
	}

	// Edge lies in the (shared) meridian plane.
	edge := fivePost.sub(sevenPost)
	if off := edge.dot(meridianPerp); off < -1e-9 || off > 1e-9 {
		t.Errorf("edge off-plane component %v (want ≈0)", off)
	}
}

// buildChainFixture wires the DIRECTIONAL chain phiZeroFollower(6,5) [6 anchors 5]
// + phiZeroCenter(7,5) [5 drags 7] plus a mirror lock (center 2, leader 7, follower
// 3). This is the {6→5→7} chain coupled into node 3 via the existing 2↔7↔3 mirror.
func buildChainFixture() *MoveDispatch {
	centers := map[string]vec3{
		"2": {5, 5, 5},
		"3": {12, 8, 9}, // nonzero φ about 2
		"5": {0, 10, 0.5},
		"6": {0, 0, 0},
		"7": {3, 0, 0},
	}
	md := &MoveDispatch{}
	md.roots = buildRoots(centers)
	md.addPhiZeroFollowerLock("6", "5")
	md.addPhiZeroCenterLock("7", "5")
	md.addMirrorLock("2", "7", "3")
	return md
}

// Chaining down: dragging node 6 (anchor) moves 5 (6→5), 7 (5→7) and 3 (7→3 mirror).
// 7 moving proves the chain reached past the directly-referenced follower.
func TestChainDrag6MovesAll(t *testing.T) {
	md := buildChainFixture()
	moved := md.applyLocks("6", true)
	for _, id := range []string{"5", "7", "3"} {
		if _, ok := moved[id]; !ok {
			t.Errorf("drag 6: node %s did not move (chain broken); moved=%v", id, moved)
		}
	}
	// Move-at-most-once guard: dragged node 6 is never in the moved set.
	if _, ok := moved["6"]; ok {
		t.Errorf("dragged node 6 should not appear in moved set")
	}
}

// Drag the tail (node 7) perpendicular to the meridian: the trio members 5 and 6 are
// CARRIED onto node 7's new meridian (perpendicular-carry — the distinct claim), and
// node 3 follows via the 7→3 mirror. In-plane, 5/6 keep their φ-plane positions; only
// their perpendicular coordinate adopts node 7's.
func TestChainDrag7CarriesFiveSixAndMirrorThree(t *testing.T) {
	md := buildChainFixture()
	// Drag node 7 to a position with a clear perpendicular (z) component.
	w7, _ := md.roots.world("7")
	md.roots.roots["7"] = rootFromCartesian(w7.add(vec3{2, 1, 6}), md.roots.origin)
	moved := md.applyLocks("7", true)
	if _, ok := moved["3"]; !ok {
		t.Errorf("drag 7: node 3 did not follow (mirror broken); moved=%v", moved)
	}
	seven, _ := md.roots.world("7")
	for _, id := range []string{"5", "6"} {
		w, _ := md.roots.world(id)
		if d := w.dot(meridianPerp) - seven.dot(meridianPerp); d < -1e-9 || d > 1e-9 {
			t.Errorf("drag 7: node %s perp=%v != node 7 perp=%v (not carried onto 7's meridian)",
				id, w.dot(meridianPerp), seven.dot(meridianPerp))
		}
	}
}

// Drag the mid (node 5) perpendicular to the meridian: node 7 follows (5 drags 7) and
// 3 follows via the mirror; node 6 is now CARRIED onto node 5's new meridian
// (perpendicular-carry — the distinct claim). The in-plane chain is unchanged.
func TestChainDrag5MovesSevenCarriesSix(t *testing.T) {
	md := buildChainFixture()
	w5, _ := md.roots.world("5")
	md.roots.roots["5"] = rootFromCartesian(w5.add(vec3{2, 1, 6}), md.roots.origin)
	moved := md.applyLocks("5", true)
	for _, id := range []string{"7", "3"} {
		if _, ok := moved[id]; !ok {
			t.Errorf("drag 5: node %s did not move (chain broken); moved=%v", id, moved)
		}
	}
	five, _ := md.roots.world("5")
	for _, id := range []string{"6", "7"} {
		w, _ := md.roots.world(id)
		if d := w.dot(meridianPerp) - five.dot(meridianPerp); d < -1e-9 || d > 1e-9 {
			t.Errorf("drag 5: node %s perp=%v != node 5 perp=%v (not carried onto 5's meridian)",
				id, w.dot(meridianPerp), five.dot(meridianPerp))
		}
	}
}

// buildFullFixture replicates the real loaded topology's locks: the θ-lock pair
// (1,2,6) [leader 2] and (1,6,2) [leader 6; node 6 LEADING carries node 2 — fires
// only when node 6 itself is placed, e.g. the node-3 authority flip], the mirror
// pair (2,3,7)/(2,7,3), and the DIRECTIONAL meridian chain phiZeroFollower(6,5)
// [6 anchors 5] + phiZeroCenter(7,5) [5 drags 7].
func buildFullFixture() *MoveDispatch {
	centers := map[string]vec3{
		"1": {0, 0, 0},
		"2": {10, 2, 5},
		"6": {9, -1, -4},
		"3": {12, 8, 9},
		"7": {8, 7, -3},
		"5": {6, 15, 1},
	}
	md := &MoveDispatch{}
	md.roots = buildRoots(centers)
	md.addThetaLock("1", "2", "6")
	md.addThetaLock("1", "6", "2")
	md.addMirrorLock("2", "3", "7")
	md.addMirrorLock("2", "7", "3")
	md.addPhiZeroFollowerLock("6", "5")
	md.addPhiZeroCenterLock("7", "5")
	return md
}

// movedMag returns the world-distance node id moved from its pre-drag position, or 0.
func movedMag(pre vec3, moved map[string]vec3, id string) float64 {
	nw, ok := moved[id]
	if !ok {
		return 0
	}
	return dist3(pre, nw)
}

// Drag 6 (anchor): the meridian chain 6→5→7 moves both 5 and 7, and 7 (mirror leader)
// then moves 3. Node 2 FOLLOWS node 6 via the restored θ-lock(1,6,2) (node 6 leading).
func TestFullDrag6MovesFiveSevenThreeAndTwo(t *testing.T) {
	md := buildFullFixture()
	pre5, _ := md.roots.world("5")
	pre7, _ := md.roots.world("7")
	pre3, _ := md.roots.world("3")
	pre2, _ := md.roots.world("2")
	moved := md.applyLocks("6", true)
	if m := movedMag(pre5, moved, "5"); m < 1e-3 {
		t.Errorf("drag 6: node 5 moved by %v (want sizable); moved=%v", m, moved)
	}
	if m := movedMag(pre7, moved, "7"); m < 1e-3 {
		t.Errorf("drag 6: node 7 moved by %v (want sizable); moved=%v", m, moved)
	}
	if m := movedMag(pre3, moved, "3"); m < 1e-3 {
		t.Errorf("drag 6: node 3 moved by %v (want sizable); moved=%v", m, moved)
	}
	if m := movedMag(pre2, moved, "2"); m < 1e-3 {
		t.Errorf("drag 6: node 2 must FOLLOW node 6 by %v (want sizable); moved=%v", m, moved)
	}
}

// Mirror leader 3 drives follower 7.
func TestFullDrag3MovesSeven(t *testing.T) {
	md := buildFullFixture()
	moved := md.applyLocks("3", true)
	if _, ok := moved["7"]; !ok {
		t.Errorf("drag 3: node 7 did not follow (mirror leader 3); moved=%v", moved)
	}
}

// Drag 7 (tail) perpendicular: node 3 follows via the mirror; nodes 5 and 6 are
// CARRIED onto node 7's new meridian (perpendicular-carry).
func TestFullDrag7MovesThreeCarriesFiveSix(t *testing.T) {
	md := buildFullFixture()
	w7, _ := md.roots.world("7")
	md.roots.roots["7"] = rootFromCartesian(w7.add(vec3{2, 1, 6}), md.roots.origin)
	moved := md.applyLocks("7", true)
	if _, ok := moved["3"]; !ok {
		t.Errorf("drag 7: node 3 did not follow (mirror leader 7); moved=%v", moved)
	}
	seven, _ := md.roots.world("7")
	for _, id := range []string{"5", "6"} {
		w, _ := md.roots.world(id)
		if d := w.dot(meridianPerp) - seven.dot(meridianPerp); d < -1e-9 || d > 1e-9 {
			t.Errorf("drag 7: node %s perp=%v != node 7 perp=%v (not carried onto 7's meridian)",
				id, w.dot(meridianPerp), seven.dot(meridianPerp))
		}
	}
}

// Drag 5 (mid) perpendicular: node 7 follows (5 drags 7); node 6 is CARRIED onto node
// 5's new meridian (perpendicular-carry); node 2 stays (no back-coupling to node 2).
func TestFullDrag5MovesSevenCarriesSixNotTwo(t *testing.T) {
	md := buildFullFixture()
	w5, _ := md.roots.world("5")
	md.roots.roots["5"] = rootFromCartesian(w5.add(vec3{2, 1, 6}), md.roots.origin)
	moved := md.applyLocks("5", true)
	if _, ok := moved["7"]; !ok {
		t.Errorf("drag 5: node 7 did not follow; moved=%v", moved)
	}
	if _, ok := moved["2"]; ok {
		t.Errorf("drag 5: node 2 must NOT move; moved=%v", moved)
	}
	five, _ := md.roots.world("5")
	for _, id := range []string{"6", "7"} {
		w, _ := md.roots.world(id)
		if d := w.dot(meridianPerp) - five.dot(meridianPerp); d < -1e-9 || d > 1e-9 {
			t.Errorf("drag 5: node %s perp=%v != node 5 perp=%v (not carried onto 5's meridian)",
				id, w.dot(meridianPerp), five.dot(meridianPerp))
		}
	}
}

// Authority FLIP when node 3 is the driver: dragging node 3 moves node 7 via the
// mirror (node 7 KEEPS its mirror radius), and node 6 FOLLOWS — rescaled about node 5
// so |6→5| == |7→5|. Node 6 moving carries node 2 (θ-lock(1,6,2)). Node 5 (Mid) stays.
// This is the opposite of the default direction (node 6 authority, node 7 follows).
func TestFullDrag3FlipsAuthorityNodeSixFollows(t *testing.T) {
	md := buildEqualRadiiFixture() // seeded equal-radii from the anchor (node 6)

	pre2, _ := md.roots.world("2")
	pre5, _ := md.roots.world("5")
	pre6, _ := md.roots.world("6")
	pre7, _ := md.roots.world("7")
	if d := dist3(pre6, pre5) - dist3(pre7, pre5); d < -1e-6 || d > 1e-6 {
		t.Fatalf("seed not equal-radii: |6→5|=%v |7→5|=%v", dist3(pre6, pre5), dist3(pre7, pre5))
	}

	// Drag node 3.
	w3, _ := md.roots.world("3")
	md.roots.roots["3"] = rootFromCartesian(w3.add(vec3{8, 5, 6}), md.roots.origin)
	moved := md.applyLocks("3", true)

	post5, _ := md.roots.world("5")
	post6, _ := md.roots.world("6")
	post7, _ := md.roots.world("7")

	// Equal-radii re-established with node 7 as the authority.
	if d := dist3(post6, post5) - dist3(post7, post5); d < -1e-6 || d > 1e-6 {
		t.Errorf("drag 3: |6→5|=%v != |7→5|=%v (equal-radii lapsed)", dist3(post6, post5), dist3(post7, post5))
	}
	// Node 6 FOLLOWED node 7's radius (it moved).
	if m := movedMag(pre6, moved, "6"); m < 1e-3 {
		t.Errorf("drag 3: node 6 must FOLLOW node 7 by %v (want sizable); moved=%v", m, moved)
	}
	// Node 2 FOLLOWED node 6.
	if m := movedMag(pre2, moved, "2"); m < 1e-3 {
		t.Errorf("drag 3: node 2 must FOLLOW node 6 by %v (want sizable); moved=%v", m, moved)
	}
	// Mid (node 5) stays.
	if d := dist3(pre5, post5); d > 1e-9 {
		t.Errorf("drag 3: node 5 moved by %v (Mid must not move)", d)
	}
	// Node 7 actually moved (mirror).
	if m := movedMag(pre7, moved, "7"); m < 1e-3 {
		t.Errorf("drag 3: node 7 moved by %v (want sizable); moved=%v", m, moved)
	}
}

// Lift node 7 leaves node 6 and node 2 put: dragging node 7 carries 5/6 onto its
// meridian via the perpendicular pre-pass (direct write, NOT placed), so node 6 is
// never enqueued and the directional θ-lock(1,6,2) never fires — node 2 stays.
func TestFullDrag7LeavesSixAndTwoPut(t *testing.T) {
	md := buildEqualRadiiFixture()
	pre2, _ := md.roots.world("2")
	w7, _ := md.roots.world("7")
	md.roots.roots["7"] = rootFromCartesian(w7.add(vec3{8, 5, 6}), md.roots.origin)
	moved := md.applyLocks("7", true)
	if _, ok := moved["2"]; ok {
		t.Errorf("lift 7: node 2 must NOT move; moved=%v", moved)
	}
	post2, _ := md.roots.world("2")
	if d := dist3(pre2, post2); d > 1e-9 {
		t.Errorf("lift 7: node 2 moved by %v (want 0)", d)
	}
}

// Center-only move (isolated): node 2 is the CENTER of mirror(2,3,7)/(2,7,3) but the
// LEADER of neither. With ONLY the mirror locks present (no θ-lock/meridian chain to
// reach 3/7 by another path), moving 2 alone must NOT drag 3 or 7 — the spurious
// center-triggered mirror fire that this fix removes. (Under the old leader-OR-center
// rule this moved both 3 and 7.)
func TestCenterTwoDoesNotDragMirror(t *testing.T) {
	centers := map[string]vec3{
		"2": {10, 2, 5},
		"3": {12, 8, 9},
		"7": {8, 7, -3},
	}
	md := &MoveDispatch{}
	md.roots = buildRoots(centers)
	md.addMirrorLock("2", "3", "7")
	md.addMirrorLock("2", "7", "3")
	moved := md.applyLocks("2", true)
	for _, id := range []string{"3", "7"} {
		if _, ok := moved[id]; ok {
			t.Errorf("center 2 moved: node %s should NOT be dragged by the mirror (leader-only rule); moved=%v", id, moved)
		}
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
	md.addPhiZeroCenterLock("7", "5")

	before, ok := md.roots.surfaceCoord("7", "5")
	if !ok {
		t.Fatal("surfaceCoord(7,5) not resolvable before lock")
	}
	if before.Phi > -1e-9 && before.Phi < 1e-9 {
		t.Fatalf("fixture invalid: φ should be clearly nonzero, got %v", before.Phi)
	}

	md.applyLocks("7", true) // drag center 7 → node 7 re-projects onto its meridian (5 stays)

	after, ok := md.roots.surfaceCoord("7", "5")
	if !ok {
		t.Fatal("surfaceCoord(7,5) not resolvable after lock")
	}
	if after.Phi < -1e-6 || after.Phi > 1e-6 {
		t.Errorf("φ not in meridian plane: got %v (want ≈0)", after.Phi)
	}
}

// radius5 is the radius of node id measured about node 5 (5's local frame).
func radius5(md *MoveDispatch, id string) float64 {
	p, _ := md.roots.surfaceCoord("5", id)
	return p.R
}

// buildEqualRadiiFixture is the full topology fixture plus the equal-radii lock
// (A=6 anchor radius authority, B=7 equalized), seeded once at load from the anchor
// (applyLocks("6")) exactly like the loader does.
func buildEqualRadiiFixture() *MoveDispatch {
	md := buildFullFixture()
	md.addEqualRadiiLock("5", "6", "7")
	md.applyLocks("6", false) // load seed from the anchor, not a drag
	return md
}

// At load the two edge radii into node 5 are equalized: |6→5| == |7→5|.
func TestEqualRadiiLockLoadEqual(t *testing.T) {
	md := buildEqualRadiiFixture()
	r6, r7 := radius5(md, "6"), radius5(md, "7")
	if math.Abs(r6-r7) > 1e-6 {
		t.Errorf("load: radii not equal: |6->5|=%v |7->5|=%v", r6, r7)
	}
}

// Dragging any of 5/6/7 keeps the two radii into node 5 equal (the in-plane equal-radii
// claim, UNCHANGED) while the perpendicular-carry claim is layered on top:
//   - drag 6 → 5,7,3 move (chain down); node 2 FOLLOWS node 6 via θ-lock(1,6,2).
//   - drag 5 → 7,3 move; node 6 is CARRIED onto 5's meridian; node 2 stays.
//   - drag 7 → 3 moves (mirror); nodes 5,6 are CARRIED onto 7's meridian; node 2 stays.
// In every case the two edge radii into node 5 stay equal and finite (no NaN).
func TestEqualRadiiLockDragKeepsEqualNoRegression(t *testing.T) {
	type expect struct {
		drag    string
		moves   []string // followers that must move via the chain/mirror
		carried []string // trio members that must land on the dragged node's meridian
		stays   []string // nodes that must not move at all
	}
	cases := []expect{
		{"6", []string{"5", "7", "3", "2"}, nil, nil},
		{"5", []string{"7", "3"}, []string{"6", "7"}, []string{"2"}},
		{"7", []string{"3"}, []string{"5", "6"}, []string{"2"}},
	}
	for _, c := range cases {
		md := buildEqualRadiiFixture()
		w, _ := md.roots.world(c.drag)
		md.roots.roots[c.drag] = rootFromCartesian(w.add(vec3{8, 5, 6}), md.roots.origin)
		moved := md.applyLocks(c.drag, true)
		r6, r7 := radius5(md, "6"), radius5(md, "7")
		if math.Abs(r6-r7) > 1e-6 {
			t.Errorf("drag %s: radii not equal: |6->5|=%v |7->5|=%v", c.drag, r6, r7)
		}
		if math.IsNaN(r6) || math.IsNaN(r7) {
			t.Errorf("drag %s: NaN radius", c.drag)
		}
		for _, id := range c.moves {
			if _, ok := moved[id]; !ok {
				t.Errorf("drag %s: expected node %s to move (regression); moved=%v", c.drag, id, moved)
			}
		}
		dw, _ := md.roots.world(c.drag)
		for _, id := range c.carried {
			w, _ := md.roots.world(id)
			if d := w.dot(meridianPerp) - dw.dot(meridianPerp); d < -1e-9 || d > 1e-9 {
				t.Errorf("drag %s: node %s perp=%v != dragged perp=%v (not carried)",
					c.drag, id, w.dot(meridianPerp), dw.dot(meridianPerp))
			}
		}
		for _, id := range c.stays {
			if _, ok := moved[id]; ok {
				t.Errorf("drag %s: node %s must NOT move; moved=%v", c.drag, id, moved)
			}
		}
	}
}

// Perpendicular-carry, node 5 dragged (the distinct claim): dragging node 5 with a
// clear perpendicular-to-meridian component lets node 5 ACTUALLY move perpendicular
// (its off-plane component changes — it is NOT dropped/pinned), and nodes 6 and 7 adopt
// node 5's new meridian (their perpendicular coordinates match node 5's — they came
// along), exactly like node 6 carries the trio.
func TestPerpendicularCarryDrag5(t *testing.T) {
	md := buildEqualRadiiFixture()
	pre5, _ := md.roots.world("5")
	// Drag node 5 with a sizable perpendicular (z) component.
	md.roots.roots["5"] = rootFromCartesian(pre5.add(vec3{1, 2, 7}), md.roots.origin)
	md.applyLocks("5", true)

	five, _ := md.roots.world("5")
	// Node 5 actually moved perpendicular (off-plane component changed; NOT dropped).
	if d := five.dot(meridianPerp) - pre5.dot(meridianPerp); d > -1 && d < 1 {
		t.Errorf("node 5 perpendicular component barely changed (%v); the drop was not removed", d)
	}
	// Nodes 6 and 7 adopted node 5's new meridian.
	for _, id := range []string{"6", "7"} {
		w, _ := md.roots.world(id)
		if d := w.dot(meridianPerp) - five.dot(meridianPerp); d < -1e-9 || d > 1e-9 {
			t.Errorf("node %s perp=%v != node 5 perp=%v (did not come onto 5's meridian)",
				id, w.dot(meridianPerp), five.dot(meridianPerp))
		}
	}
	// In-plane equal-radii preserved.
	r6, r7 := radius5(md, "6"), radius5(md, "7")
	if math.Abs(r6-r7) > 1e-6 || math.IsNaN(r6) || math.IsNaN(r7) {
		t.Errorf("drag 5: radii not equal/finite: |6->5|=%v |7->5|=%v", r6, r7)
	}
}

// Perpendicular-carry, node 7 dragged (the distinct claim, 7↔5 mirror of the drag-5
// case): node 7 moves perpendicular; nodes 5 and 6 come onto node 7's new meridian.
func TestPerpendicularCarryDrag7(t *testing.T) {
	md := buildEqualRadiiFixture()
	pre7, _ := md.roots.world("7")
	md.roots.roots["7"] = rootFromCartesian(pre7.add(vec3{1, 2, 7}), md.roots.origin)
	md.applyLocks("7", true)

	seven, _ := md.roots.world("7")
	// Node 7 actually moved perpendicular.
	if d := seven.dot(meridianPerp) - pre7.dot(meridianPerp); d > -1 && d < 1 {
		t.Errorf("node 7 perpendicular component barely changed (%v); the drop was not removed", d)
	}
	// Nodes 5 and 6 adopted node 7's new meridian.
	for _, id := range []string{"5", "6"} {
		w, _ := md.roots.world(id)
		if d := w.dot(meridianPerp) - seven.dot(meridianPerp); d < -1e-9 || d > 1e-9 {
			t.Errorf("node %s perp=%v != node 7 perp=%v (did not come onto 7's meridian)",
				id, w.dot(meridianPerp), seven.dot(meridianPerp))
		}
	}
	// In-plane equal-radii preserved.
	r6, r7 := radius5(md, "6"), radius5(md, "7")
	if math.Abs(r6-r7) > 1e-6 || math.IsNaN(r6) || math.IsNaN(r7) {
		t.Errorf("drag 7: radii not equal/finite: |6->5|=%v |7->5|=%v", r6, r7)
	}
}

// In-plane drag (no perpendicular component) is UNCHANGED by the carry: dragging node 5
// within the meridian (after the co-planar load seed) leaves node 6's perpendicular
// coordinate alone and does not enter node 6 into the moved set — only the in-plane
// chain (5 drags 7, mirror to 3) fires.
func TestInPlaneDrag5DoesNotCarrySix(t *testing.T) {
	md := buildEqualRadiiFixture()
	pre6, _ := md.roots.world("6")
	w5, _ := md.roots.world("5")
	// In-plane delta: zero perpendicular (z) component.
	md.roots.roots["5"] = rootFromCartesian(w5.add(vec3{6, 4, 0}), md.roots.origin)
	moved := md.applyLocks("5", true)

	if _, ok := moved["6"]; ok {
		t.Errorf("in-plane drag 5: node 6 must NOT move (no perpendicular carry); moved=%v", moved)
	}
	post6, _ := md.roots.world("6")
	if d := dist3(pre6, post6); d > 1e-9 {
		t.Errorf("in-plane drag 5: node 6 moved by %v (want 0)", d)
	}
	if _, ok := moved["7"]; !ok {
		t.Errorf("in-plane drag 5: node 7 did not follow the chain; moved=%v", moved)
	}
	r6, r7 := radius5(md, "6"), radius5(md, "7")
	if math.Abs(r6-r7) > 1e-6 {
		t.Errorf("in-plane drag 5: radii not equal: |6->5|=%v |7->5|=%v", r6, r7)
	}
}
