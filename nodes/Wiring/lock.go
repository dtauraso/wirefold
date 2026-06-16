package Wiring

// lock.go — polar layout locks (docs/planning/visual-editor/polar-coordinate-model.md
// §4, §7). A lock is a RELATIONSHIP between roots, applied after a RootMove —
// not stored secondary state. The canonical example is the perpendicular-chord
// lock: a follower node mirrors a leader across a center's vertical disk
// (same r, same θ, opposite φ in the center's local frame, pole = +y).

// chordLock binds Follower to mirror Leader across Center's vertical (φ=0) disk.
type chordLock struct {
	Center   string
	Leader   string
	Follower string
}

// addChordLock registers a chord lock.
func (md *MoveDispatch) addChordLock(center, leader, follower string) {
	md.locks = append(md.locks, chordLock{Center: center, Leader: leader, Follower: follower})
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
		// Leader relative to the center (local frame, pole +y), then mirror φ.
		lp, ok := md.roots.surfaceCoord(lk.Center, lk.Leader)
		if !ok {
			continue
		}
		mirror := polar{R: lp.R, Theta: lp.Theta, Phi: -lp.Phi}
		fw := polar2cart(mirror).add(cw) // follower world position
		md.roots.roots[lk.Follower] = rootFromCartesian(fw, md.roots.origin)

		// Fan the follower (new center) + recompute reach so any sphere it sits on
		// re-emits its grown ring.
		centers := md.heldCenters()
		centers[lk.Follower] = fw
		reach := reachRFromCenters(centers, md.heldEdges())
		md.fanCenters(map[string]vec3{lk.Follower: fw}, reach)
	}
}
