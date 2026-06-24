package Wiring

import "fmt"

// lock.go — polar layout locks (docs/planning/visual-editor/polar-coordinate-model.md
// §4, §7). A lock is a RELATIONSHIP between roots, applied after a RootMove —
// not stored secondary state. The active example is the theta lock: a follower
// node shares a leader's θ (the angle from the center's +y up-pole) while keeping
// its own azimuth φ and radius. So the two stay on the same latitude ring around
// the center (same angle-from-pole), at their own longitudes.

// thetaLock binds Follower to share Leader's θ about Center (pole = +y), keeping
// the Follower's own φ and r. Equalizes the angle-from-the-up-pole for the pair.
type thetaLock struct {
	Center   string
	Leader   string
	Follower string
}

// addThetaLock registers a theta lock.
func (md *MoveDispatch) addThetaLock(center, leader, follower string) {
	md.locks = append(md.locks, thetaLock{Center: center, Leader: leader, Follower: follower})
}

// logPairTheta (diagnostic) emits θ of nodes 3 and 7 about node 2 after a move —
// for EVERY RootMove, whether or not a lock fired. Unlike the per-lock breadcrumb
// (which is tautological), this catches a follower-drag that never fires the lock
// (you'd see moved=7 with th3≠th7) and any real divergence in the roots.
func (md *MoveDispatch) logPairTheta(movedID string) {
	if md.tr == nil {
		return
	}
	t3, ok3 := md.roots.surfaceCoord("2", "3")
	t7, ok7 := md.roots.surfaceCoord("2", "7")
	if !ok3 || !ok7 {
		return
	}
	md.tr.Breadcrumb("pair_theta", movedID, "",
		fmt.Sprintf("moved=%s th3=%.4f th7=%.4f d=%.4f", movedID, t3.Theta, t7.Theta, t3.Theta-t7.Theta))
}

// applyLocks re-derives any follower whose lock references the moved node
// (as leader or center), updating the follower's root + center and fanning it.
// Soft membership is preserved: only locked followers move, derived from roots.
func (md *MoveDispatch) applyLocks(movedID string) {
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
		// Lock: the follower adopts the leader's θ (angle from the +y up-pole) and
		// keeps its OWN azimuth φ and radius. Both then sit at the same angle-from-pole
		// (same latitude ring) on opposite/own longitudes — not mirrored across a disk.
		locked := polar{R: fp.R, Theta: lp.Theta, Phi: fp.Phi}
		fw := polar2cart(locked).add(cw) // follower world position
		md.roots.roots[lk.Follower] = rootFromCartesian(fw, md.roots.origin)

		// Fan the follower (new center) + recompute reach so any sphere it sits on
		// re-emits its grown ring.
		centers := md.heldCenters()
		centers[lk.Follower] = fw
		reach := reachRFromCenters(centers, md.heldEdges())
		md.fanCenters(map[string]vec3{lk.Follower: fw}, reach)

		// DIAGNOSTIC (task/theta-lock-diag): record what the lock did. leadTh is the
		// θ we copied; follAfter is the follower's θ re-derived from its new position.
		// If follAfter != leadTh the lock didn't stick; if no theta_lock breadcrumbs
		// appear during a drag, applyLocks isn't firing for that move.
		if md.tr != nil {
			af, _ := md.roots.surfaceCoord(lk.Center, lk.Follower)
			md.tr.Breadcrumb("theta_lock", lk.Follower, lk.Leader,
				fmt.Sprintf("moved=%s leadTh=%.4f follBefore=%.4f follAfter=%.4f",
					movedID, lp.Theta, fp.Theta, af.Theta))
		}
	}
}
