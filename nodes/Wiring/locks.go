package Wiring

// locks.go — the rebuilt lock engine: polar equations that ride on the double-link
// movement graph (links.go). No central store — node world positions come from the
// movers' held geometry; polar coordinates are derived on the fly with cart2polar /
// polar2cart. Locks are reintroduced ONE record at a time (see
// docs/planning/visual-editor/existing-lock-system-record.md).

// nodeCenter returns a node's current world position from its mover's held geometry.
func (md *MoveDispatch) nodeCenter(id string) (vec3, bool) {
	m, ok := md.nodeMovers[id]
	if !ok || m.geom.Center == nil {
		return vec3{}, false
	}
	return *m.geom.Center, true
}

// surfaceCoord returns the polar coordinate (R, θ, φ) of surfacePos in the frame whose
// origin is centerPos (pole = +y). This is the "radius-point" of one node on another's
// surface — the double link expressed in polar.
func surfaceCoord(centerPos, surfacePos vec3) polar {
	return cart2polar(surfacePos.sub(centerPos))
}

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
// is movedID. pos resolves a node's current world position (the caller seeds it with the
// dragged node's target so the leader reads its NEW position).
func (md *MoveDispatch) applyMirrorLocks(movedID string, pos func(string) (vec3, bool)) map[string]vec3 {
	out := map[string]vec3{}
	for _, lk := range md.mirrorLocks {
		if lk.Leader != movedID {
			continue
		}
		c, okC := pos(lk.Center)
		lw, okL := pos(lk.Leader)
		fw, okF := pos(lk.Follower)
		if !okC || !okL || !okF {
			continue
		}
		_ = fw                   // follower must exist; its old position is not used
		l := surfaceCoord(c, lw) // leader's polar about center
		// True mirror: follower takes the LEADER's radius (so the two stay equidistant
		// from Center), the leader's θ, and the opposite φ — a reflection across the
		// Center's φ=0 meridian.
		locked := polar{R: l.R, Theta: l.Theta, Phi: -l.Phi}
		out[lk.Follower] = polar2cart(locked).add(c)
	}
	return out
}
