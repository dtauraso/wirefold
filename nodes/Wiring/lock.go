package Wiring

import "fmt"

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
	for _, lk := range md.locks {
		if lk.Leader != movedID && lk.Center != movedID {
			continue
		}
		cw, ok := md.roots.world(lk.Center)
		if !ok {
			continue
		}
		// Leader and follower in the center's local frame (pole +y).
		lp, ok := md.roots.surfaceCoord(lk.Center, lk.Leader)
		if !ok {
			continue
		}
		fp, ok := md.roots.surfaceCoord(lk.Center, lk.Follower)
		if !ok {
			continue
		}
		// Lock: the follower adopts the leader's θ (angle from the +y up-pole) and keeps
		// its own radius. φ is either the follower's OWN (same latitude ring, own
		// longitude) or, for a mirror lock, −leaderφ (opposite-sign, equal-magnitude
		// longitude — reflected across the φ=0 meridian).
		phi := fp.Phi
		if lk.MirrorPhi {
			phi = -lp.Phi
		}
		locked := polar{R: fp.R, Theta: lp.Theta, Phi: phi}
		fw := polar2cart(locked).add(cw) // follower world position
		md.roots.roots[lk.Follower] = rootFromCartesian(fw, md.roots.origin)
		moved[lk.Follower] = fw
	}
	return moved
}
