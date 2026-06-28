package Wiring

import (
	"fmt"
	"math"
)

// lock.go — polar layout locks (docs/planning/visual-editor/polar-coordinate-model.md
// §4, §7). A lock is a RELATIONSHIP between roots, applied after a RootMove —
// not stored secondary state. The active example is the theta lock: a follower
// node shares a leader's θ (the angle from the center's +y up-pole) while keeping
// its own azimuth φ and radius. So the two stay on the same latitude ring around
// the center (same angle-from-pole), at their own longitudes.

// thetaLock binds Follower to share Leader's θ about Center (pole = +y), keeping
// the Follower's own r. By default it keeps the Follower's own φ (same latitude
// ring, own longitude). When MirrorPhi is set it instead gives the Follower
// φ = −Leaderφ — a reflection across the φ=0 (+x) meridian, so the pair has
// equal-magnitude, opposite-sign longitude (same θ, mirrored φ).
type thetaLock struct {
	Center    string
	Leader    string
	Follower  string
	MirrorPhi bool
}

// addThetaLock registers a theta lock (shared θ, follower keeps its own φ).
func (md *MoveDispatch) addThetaLock(center, leader, follower string) {
	md.locks = append(md.locks, thetaLock{Center: center, Leader: leader, Follower: follower})
}

// addMirrorLock registers a theta lock that also mirrors φ: the follower shares the
// leader's θ and takes φ = −leaderφ (opposite-sign, equal-magnitude longitude).
func (md *MoveDispatch) addMirrorLock(center, leader, follower string) {
	md.locks = append(md.locks, thetaLock{Center: center, Leader: leader, Follower: follower, MirrorPhi: true})
}

// phiDrive selects which of the two coupled nodes a φ=0 lock is allowed to WRITE,
// giving the meridian coupling a DIRECTION (no symmetric back-coupling):
//
//   - moveFollower: anchor the Center. The lock only ever writes the Follower —
//     when the Center moves, project the Follower onto the Center's φ=0 meridian;
//     when the Follower itself moves, re-project it onto the Center's meridian.
//     The Center is NEVER written by this lock. (Used for 6→5: node 6 anchors 5.)
//   - moveCenter: the Follower drives the Center. The lock only ever writes the
//     Center — when the Follower moves, move the Center to keep the Follower on the
//     Center's φ=0 meridian; when the Center itself is dragged, re-project the
//     Center (the dragged node) onto its own meridian about the Follower. The
//     Follower is NEVER written by this lock. (Used for 5→7: node 5 drags 7.)
type phiDrive int

const (
	moveFollower phiDrive = iota
	moveCenter
)

// phiZeroLock couples Follower and Center on a φ=0 meridian (Center's local frame,
// pole = +y): the coupled edge lies in the meridian plane (off-plane component = 0).
// Drive selects which single node the lock may write (see phiDrive) so the coupling
// is directional — only one side moves per lock, never both.
type phiZeroLock struct {
	Center   string
	Follower string
	Drive    phiDrive
}

// addPhiZeroFollowerLock registers a meridian lock that anchors the Center and
// writes only the Follower (moveFollower). Used for 6→5: node 6 anchors node 5.
func (md *MoveDispatch) addPhiZeroFollowerLock(center, follower string) {
	md.phiZeroLocks = append(md.phiZeroLocks, phiZeroLock{Center: center, Follower: follower, Drive: moveFollower})
}

// addPhiZeroCenterLock registers a meridian lock where the Follower drives the
// Center and writes only the Center (moveCenter). Used for 5→7: node 5 drags node 7.
func (md *MoveDispatch) addPhiZeroCenterLock(center, follower string) {
	md.phiZeroLocks = append(md.phiZeroLocks, phiZeroLock{Center: center, Follower: follower, Drive: moveCenter})
}

// equalRadiiLock keeps the two edge radii into Mid equal: r(A about Mid) ==
// r(B about Mid), measured in Mid's local frame (pole = +y). It is a pure-polar
// radius equalization: only B's R changes. In the DIRECTIONAL chain (6 anchors 5
// drags 7) A is the permanent radius AUTHORITY (the anchor, node 6) and B is always
// the equalized node (node 7); the authority never flips to the dragged node, since
// flipping would let a drag move the anchor. So A's radius about Mid is the
// reference, and B is rescaled to it on every drag (6, 5, or 7).
//
// This lock does NOT introduce a separate place(): node B (7) is already written by
// the moveCenter φ=0 lock (5 drives 7) and projected onto the meridian plane. The
// radius equalization is FOLDED into that projection — one combined move (project
// onto the meridian plane, then scale to A's radius about Mid). Scaling about Mid
// preserves direction, so the in-plane projection is retained; the two locks touch
// different polar components (φ-plane vs R) and compose cleanly in a single write.
type equalRadiiLock struct {
	Mid string
	A   string
	B   string
}

// addEqualRadiiLock registers an equal-radii lock (r(A about Mid) == r(B about Mid)).
// A is the radius authority (anchor side); B is the equalized side.
func (md *MoveDispatch) addEqualRadiiLock(mid, a, b string) {
	md.equalRadiiLocks = append(md.equalRadiiLocks, equalRadiiLock{Mid: mid, A: a, B: b})
}

// equalRadiiAdjust, given that the φ=0 lock is about to write `other` to world
// position nw (already projected onto Mid's meridian plane), returns the adjusted
// world position that also makes r(other about Mid) equal to A's radius about Mid.
// It only fires when `other` is B (the equalized side); A (the authority/anchor)
// is never rescaled. Returns (nw, false) when no equal-radii lock applies.
func (md *MoveDispatch) equalRadiiAdjust(other string, nw vec3) (vec3, bool) {
	for _, lk := range md.equalRadiiLocks {
		if other != lk.B {
			continue
		}
		mw, ok := md.roots.world(lk.Mid)
		if !ok {
			continue
		}
		rp, ok := md.roots.surfaceCoord(lk.Mid, lk.A)
		if !ok {
			continue
		}
		// Direction from Mid to the already-projected position; scale to A's
		// radius. Pure-polar: keeps θ/φ about Mid (in-plane), sets R.
		dir := nw.sub(mw)
		if dir.length() == 0 {
			continue
		}
		return mw.add(dir.normalize().scale(rp.R)), true
	}
	return nw, false
}

// logPairPhi (diagnostic) emits φ of nodes 3 and 7 about node 2 after a move. For a
// consistent mirror lock φ7 = −φ3, so sum = φ3+φ7 should stay ≈0; a drifting sum
// flags the inconsistency. Logged for EVERY RootMove, whether or not a lock fired.
func (md *MoveDispatch) logPairPhi(movedID string) {
	if md.tr == nil {
		return
	}
	p3, ok3 := md.roots.surfaceCoord("2", "3")
	p7, ok7 := md.roots.surfaceCoord("2", "7")
	if !ok3 || !ok7 {
		return
	}
	md.tr.Breadcrumb("pair_phi", movedID, "",
		fmt.Sprintf("moved=%s phi3=%.4f phi7=%.4f sum=%.4f th3=%.4f th7=%.4f",
			movedID, p3.Phi, p7.Phi, p3.Phi+p7.Phi, p3.Theta, p7.Theta))
}

// logPair26 (diagnostic) emits θ/φ/r of nodes 2 and 6 about node 1 after a move,
// to localize the "node 2 jumps when dragged" report. dth = θ2−θ6 should stay ≈0
// (theta lock); a jumping θ2 or r2 across a drag flags an unstable drag authority on
// node 2 (which is uniquely also the center of the 3/7 mirror lock).
func (md *MoveDispatch) logPair26(movedID string) {
	if md.tr == nil {
		return
	}
	p2, ok2 := md.roots.surfaceCoord("1", "2")
	p6, ok6 := md.roots.surfaceCoord("1", "6")
	if !ok2 || !ok6 {
		return
	}
	md.tr.Breadcrumb("pair_26", movedID, "",
		fmt.Sprintf("moved=%s th2=%.4f th6=%.4f dth=%.4f phi2=%.4f phi6=%.4f r2=%.2f r6=%.2f",
			movedID, p2.Theta, p6.Theta, p2.Theta-p6.Theta, p2.Phi, p6.Phi, p2.R, p6.R))
}

// applyLocks re-derives any follower whose lock references the moved node (as leader
// or center) and updates the follower's root in place. It does NOT fan: it returns the
// followers' new world centers so the caller (RootMove) folds them into the single
// per-frame fan. Fanning here would re-emit edges already fanned by RootMove (the
// duplicate-emit drag lag). Soft membership is preserved: only locked followers move.
func (md *MoveDispatch) applyLocks(movedID string, fromDrag bool) map[string]vec3 {
	moved := map[string]vec3{}
	// BFS fixpoint: locks chain. When a lock moves a follower, locks that reference
	// THAT follower must fire too (e.g. drag 6 → phiZeroLock(6,5) moves 5 → phiZeroLock(7,5)
	// moves 7 → mirror(2,7,3) moves 3). A node is processed at most once: the `processed`
	// guard makes each node move at most once per call, which prevents oscillation between
	// bidirectional locks and guarantees termination (each node enqueued/processed once).
	processed := map[string]bool{movedID: true}
	queue := []string{movedID}

	// place records a follower's new world position: skip if already processed (the
	// move-at-most-once guard), otherwise write the root, record it, and enqueue it.
	place := func(id string, nw vec3) {
		if processed[id] {
			return
		}
		md.roots.roots[id] = rootFromCartesian(nw, md.roots.origin)
		moved[id] = nw
		processed[id] = true
		queue = append(queue, id)
	}

	// Dragged-node meridian carry (DISTINCT CLAIM, separate from the in-plane chain):
	// when the user drags the WRITTEN node of one of its own φ=0 locks — node 5
	// (moveFollower.Follower) or node 7 (moveCenter.Center) — that node may move
	// PERPENDICULAR to the meridian, carrying the rest of the {5,6,7} trio with it,
	// exactly like node 6 already does. (Node 6 is a Center/kept of its lock, never a
	// written node, so this pre-pass never fires for it; its carry is the existing BFS
	// path and is unchanged.)
	//
	// Old behavior DROPPED the dragged node's off-plane component (`v -= perp·(v·perp)`),
	// pinning 5/7 to the existing meridian. We now INVERT which side gets projected: the
	// dragged node KEEPS its full perpendicular component (it is NOT projected), and the
	// OTHER two trio members adopt the dragged node's perpendicular coordinate — they
	// shift along perp onto the dragged node's new meridian, keeping their in-plane
	// (φ-plane) positions. The in-plane chain (6 anchors 5, 5 drags 7), equal-radii, and
	// the 3↔7 mirror are untouched and still run in the BFS below.
	//
	// In-plane drags are unaffected: a drag that stays in the meridian leaves the
	// dragged node's perpendicular coordinate equal to the others', so the shift is zero.
	// Gated by fromDrag so the load-time seed never constrains the seed node.
	if fromDrag {
		// perp is the φ=90° axis of the polar frame: the normal of the φ=0 meridian
		// plane (pole-safe — projection only, no atan2).
		perp := polar2cart(polar{R: 1, Theta: math.Pi / 2, Phi: math.Pi / 2})

		// Is the dragged node the WRITTEN node of a φ=0 lock (node 5 or 7)? Collect the
		// trio members referenced by the φ=0 locks while checking.
		isWrittenDrag := false
		trio := map[string]bool{}
		for _, lk := range md.phiZeroLocks {
			trio[lk.Center] = true
			trio[lk.Follower] = true
			if (lk.Drive == moveFollower && movedID == lk.Follower) ||
				(lk.Drive == moveCenter && movedID == lk.Center) {
				isWrittenDrag = true
			}
		}

		if dw, ok := md.roots.world(movedID); isWrittenDrag && ok {
			// targetPerp: the dragged node's perpendicular coordinate — its new
			// meridian. The dragged node KEEPS this (not dropped).
			targetPerp := dw.dot(perp)

			// Carry the meridian: every OTHER trio member adopts targetPerp (shifts
			// along perp onto the dragged node's meridian), keeping its in-plane
			// position. This is the inversion: the OTHERS get projected, not the
			// dragged node.
			for other := range trio {
				if other == movedID || processed[other] {
					continue
				}
				ow, ok := md.roots.world(other)
				if !ok {
					continue
				}
				shift := perp.scale(targetPerp - ow.dot(perp))
				if shift.length() < 1e-9 { // already on the dragged node's meridian (in-plane drag)
					continue
				}
				no := ow.add(shift)
				md.roots.roots[other] = rootFromCartesian(no, md.roots.origin)
				moved[other] = no
			}

			// In-plane (UNCHANGED claim): keep the dragged node's full position,
			// folding equal-radii if it is the equalized side (B/node 7). With every
			// trio member now on the common meridian (relative perp = 0), the radius
			// equalization measured about Mid is purely in-plane; scaling about Mid
			// preserves the perpendicular coordinate. equalRadiiAdjust is a no-op for
			// the non-equalized side (node 5).
			if adj, ok := md.equalRadiiAdjust(movedID, dw); ok {
				md.roots.roots[movedID] = rootFromCartesian(adj, md.roots.origin)
				moved[movedID] = adj
			}
		}
	}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		// θ locks: fire ONLY when `current` is the leader. A center moving is just the
		// frame origin shifting; the follower must not be independently dragged by it
		// (that spurious center-triggered path made node 6's drag nudge center 2, fire
		// the mirror on 2, and freeze node 7 via the move-once guard). The follower
		// adopts the leader's θ (angle from the +y up-pole) and keeps its own radius. φ
		// is either the follower's OWN (same latitude ring, own longitude) or, for a
		// mirror lock, −leaderφ.
		for _, lk := range md.locks {
			if lk.Leader != current {
				continue
			}
			cw, ok := md.roots.world(lk.Center)
			if !ok {
				continue
			}
			lp, ok := md.roots.surfaceCoord(lk.Center, lk.Leader)
			if !ok {
				continue
			}
			fp, ok := md.roots.surfaceCoord(lk.Center, lk.Follower)
			if !ok {
				continue
			}
			phi := fp.Phi
			if lk.MirrorPhi {
				phi = -lp.Phi
			}
			locked := polar{R: fp.R, Theta: lp.Theta, Phi: phi}
			fw := polar2cart(locked).add(cw) // follower world position
			place(lk.Follower, fw)
		}

		// φ=0 meridian locks (DIRECTIONAL): fire when `current` is the center or
		// follower. Drive picks the single WRITTEN node and the KEPT node (whose
		// meridian plane defines the projection); the lock never writes the other:
		//   - moveFollower: write the Follower, keep the Center (6 anchors 5).
		//   - moveCenter:   write the Center,   keep the Follower (5 drags 7).
		// The written node is projected onto the kept node's φ=0 meridian PLANE by
		// dropping ONLY the off-plane component (the component along the φ-perpendicular
		// axis of the polar frame). No φ, no atan2 — defined everywhere, including the
		// pole. If the written node is the dragged node (e.g. dragging the Center of a
		// moveCenter lock), place()'s move-once guard skips it here and the post-pass
		// below re-projects the dragged node itself.
		for _, lk := range md.phiZeroLocks {
			if current != lk.Center && current != lk.Follower {
				continue
			}
			var written, kept string
			switch lk.Drive {
			case moveFollower:
				written, kept = lk.Follower, lk.Center
			case moveCenter:
				written, kept = lk.Center, lk.Follower
			}
			ww, ok := md.roots.world(written)
			if !ok {
				continue
			}
			kw, ok := md.roots.world(kept)
			if !ok {
				continue
			}
			// φ=90° axis of the polar frame: the normal of the φ=0 meridian plane.
			perp := polar2cart(polar{R: 1, Theta: math.Pi / 2, Phi: math.Pi / 2})
			v := ww.sub(kw)
			v = v.sub(perp.scale(v.dot(perp))) // drop the off-plane component
			nw := kw.add(v)
			// Fold equal-radii: if `written` is the equalized side (B/node 7),
			// rescale the projected position about Mid to A's (anchor) radius. One
			// combined move (meridian plane + equal radius) in a single place().
			if adj, ok := md.equalRadiiAdjust(written, nw); ok {
				nw = adj
			}
			place(written, nw)
		}
	}

	return moved
}
