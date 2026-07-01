package Wiring

// locks.go — the rebuilt lock engine: polar equations that ride on the double-link
// movement graph (links.go). Locks read and write LINK POLAR STATE directly — no
// cart2polar reconstruction; the only world→polar conversion is refreshLink at load and
// the drag edge. World is derived from the link polar (polar2cart) for the render bridge.
// Locks are reintroduced ONE record at a time (see
// docs/planning/visual-editor/existing-lock-system-record.md).

// mirrorLock (record #1: registerNode2MirrorLocks). Follower shares Leader's θ about
// Center and takes the opposite φ (reflection across Center's φ=0 meridian), keeping its
// OWN radius. Directional: fires when Leader is the moved node, writes Follower. Register
// both directions for a bidirectional pair (e.g. mirror(2,3,7) + mirror(2,7,3)).
type mirrorLock struct {
	Center   string
	Leader   string
	Follower string
}

// addMirror registers a mirror lock (Follower mirrors Leader about Center).
func (md *MoveDispatch) addMirror(center, leader, follower string) {
	md.mirrorLocks = append(md.mirrorLocks, mirrorLock{Center: center, Leader: leader, Follower: follower})
}

// applyMirrorLocks returns the new world positions of any mirror followers whose Leader
// is movedID. It reads the leader's polar from the STORED link state (refreshed at the
// drag edge), writes the follower's polar on its link, and derives the follower's world
// only for the render bridge. The lock equation is pure polar — no cart2polar.
func (md *MoveDispatch) applyMirrorLocks(movedID string, pos func(string) (vec3, bool)) map[string]vec3 {
	out := map[string]vec3{}
	for _, lk := range md.mirrorLocks {
		if lk.Leader != movedID {
			continue
		}
		leaderLink := md.linkBetween(lk.Center, lk.Leader)
		followerLink := md.linkBetween(lk.Center, lk.Follower)
		if leaderLink == nil || followerLink == nil {
			continue
		}
		lp, ok := leaderLink.polarOf(lk.Center, lk.Leader)
		if !ok {
			continue
		}
		// True mirror across the Center's φ=0 meridian: same R, same θ, opposite φ.
		// Pure polar — written straight onto the follower link.
		fp := polar{R: lp.R, Theta: lp.Theta, Phi: -lp.Phi}
		followerLink.setPolar(lk.Center, lk.Follower, fp)
		// Derive the follower's world for the movers (polar → world). The reverse link
		// direction is recomputed by refreshLinksTouching on the next move.
		c, ok := pos(lk.Center)
		if !ok {
			continue
		}
		out[lk.Follower] = polar2cart(fp).add(c)
	}
	return out
}
