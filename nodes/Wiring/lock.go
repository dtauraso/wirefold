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

// phiZeroLock pins Follower onto Center's φ=0 meridian (Center's local frame,
// pole = +y). The follower keeps its distance R and latitude θ from the center;
// only the azimuth φ is zeroed. So the follower is moved onto the +x meridian of
// the center's frame, and any edge aimed from the center to the follower lies on
// φ=0.
type phiZeroLock struct {
	Center   string
	Follower string
}

// addPhiZeroLock registers a φ=0 meridian lock (follower keeps R and θ, φ→0).
func (md *MoveDispatch) addPhiZeroLock(center, follower string) {
	md.phiZeroLocks = append(md.phiZeroLocks, phiZeroLock{Center: center, Follower: follower})
}

// equalRadiiLock keeps the two edge radii into Mid equal: r(A about Mid) ==
// r(B about Mid), measured in Mid's local frame (pole = +y). It is a pure-polar
// radius equalization: the equalized node keeps its θ and φ about Mid and only
// its R changes. The authority is the dragged source — the non-dragged source is
// rescaled to match it. When neither source is dragged (Mid itself moved), B is
// rescaled to A's radius so the pair stays equal.
//
// This lock does NOT introduce a separate place(): nodes A and B are already
// moved by the φ=0 meridian locks (both have follower Mid; dragging any of
// Mid/A/B projects the other source onto Mid's meridian plane). The move-once
// guard would block a second place() on the same node, so the radius
// equalization is FOLDED into that φ=0 projection — one combined move per node
// (project onto the meridian plane, then scale to the sibling's radius about
// Mid). Scaling about Mid preserves direction, so the in-plane (z=Mid.z)
// projection is retained; the two locks touch different polar components (φ-plane
// vs R) and compose cleanly in a single place().
type equalRadiiLock struct {
	Mid string
	A   string
	B   string
}

// addEqualRadiiLock registers an equal-radii lock (r(A about Mid) == r(B about Mid)).
func (md *MoveDispatch) addEqualRadiiLock(mid, a, b string) {
	md.equalRadiiLocks = append(md.equalRadiiLocks, equalRadiiLock{Mid: mid, A: a, B: b})
}

// equalRadiiAdjust, given that the φ=0 lock is about to move `other` to world
// position nw (already projected onto Mid's meridian plane), returns the adjusted
// world position that also makes r(other about Mid) equal to the authoritative
// sibling's radius about Mid. movedID is the originally dragged node. It returns
// (nw, false) when no equal-radii lock applies to `other` for this drag.
//
// Authority: if movedID is one of the two sources, that source is the reference
// and the OTHER source is the one equalized. If movedID is Mid (neither source
// dragged), B is equalized to A. So the equalized node is whichever of {A,B} is
// NOT the reference, and `other` must be that equalized node.
func (md *MoveDispatch) equalRadiiAdjust(other, movedID string, nw vec3) (vec3, bool) {
	for _, lk := range md.equalRadiiLocks {
		var reference, equalized string
		switch movedID {
		case lk.A:
			reference, equalized = lk.A, lk.B
		case lk.B:
			reference, equalized = lk.B, lk.A
		case lk.Mid:
			reference, equalized = lk.A, lk.B
		default:
			continue
		}
		if other != equalized {
			continue
		}
		mw, ok := md.roots.world(lk.Mid)
		if !ok {
			continue
		}
		rp, ok := md.roots.surfaceCoord(lk.Mid, reference)
		if !ok {
			continue
		}
		// Direction from Mid to the already-projected position; scale to the
		// reference radius. Pure-polar: keeps θ/φ about Mid (in-plane), sets R.
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
func (md *MoveDispatch) applyLocks(movedID string) map[string]vec3 {
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

		// φ=0 meridian locks: fire when `current` is the center or follower. The DRAGGED
		// node (current) stays put; the OTHER node is projected onto the dragged node's φ=0
		// meridian PLANE by dropping ONLY the off-plane component (the component along the
		// φ-perpendicular axis of the polar frame). No φ, no atan2 — defined everywhere,
		// including the pole; preserves whichever side each node is already on.
		for _, lk := range md.phiZeroLocks {
			var dragged, other string
			switch current {
			case lk.Center:
				dragged, other = lk.Center, lk.Follower
			case lk.Follower:
				dragged, other = lk.Follower, lk.Center
			default:
				continue
			}
			dw, ok := md.roots.world(dragged)
			if !ok {
				continue
			}
			ow, ok := md.roots.world(other)
			if !ok {
				continue
			}
			// φ=90° axis of the polar frame: the normal of the φ=0 meridian plane.
			perp := polar2cart(polar{R: 1, Theta: math.Pi / 2, Phi: math.Pi / 2})
			v := ow.sub(dw)
			v = v.sub(perp.scale(v.dot(perp))) // drop the off-plane component
			nw := dw.add(v)
			// Fold equal-radii: if `other` is the node whose radius about Mid must
			// match its sibling, rescale the projected position about Mid. One
			// combined move (meridian plane + equal radius) in a single place().
			if adj, ok := md.equalRadiiAdjust(other, movedID, nw); ok {
				nw = adj
			}
			place(other, nw)
		}
	}
	return moved
}
