package Wiring

// lock_registry.go — per-node registration of the polar layout locks
// (docs/planning/visual-editor/polar-coordinate-model.md §7). Each lock is a
// RELATIONSHIP defined on some node's frame; this file groups the registrations so
// the locks a given node ORIGINATES are findable in one place (grep the node id or
// read its register* method) rather than buried in loader.go's startup sequence.
//
// Discoverability convention: a lock is filed under the node whose LOCAL FRAME it is
// defined on — the Center for θ/mirror locks, the meridian subject for the φ=0 chain.
// So "node 2's locks" = the locks centered on node 2's sphere (the 3/7 mirror).
//
// ORDER MATTERS and is preserved exactly: loader.go registers + seeds these in
// sequence (node-1 θ, then node-2 mirror + seed, then the 5/6/7 chain + seed). The
// node-2 seed runs BEFORE the chain locks are registered, so seeding "3" does NOT
// cascade into the chain. Do not collapse registration ahead of all seeds — that
// would let the mirror seed fire chain locks via the applyLocks BFS and change the
// load-time layout. Each method only registers; loader owns the interleaved seeding.

// registerNode1ThetaLocks couples nodes 2 and 6 on node 1's sphere via a bidirectional
// theta lock: dragging either makes the other share its θ (angle from node 1's +y
// up-pole), so the two stay on the same latitude ring around node 1 while keeping
// their own longitudes. The 6→2 direction is required so node 6 (written by the
// node-3 authority flip, where 6 follows node 7's radius) carries node 2; leader-only
// θ-lock firing keeps it directional (a 7-lift leaves node 2 put). Returns true when
// the lock was registered (all three nodes present), so the caller can install the
// matching aimed-port registry under the same guard.
func (md *MoveDispatch) registerNode1ThetaLocks(has func(string) bool) bool {
	if !(has("1") && has("2") && has("6")) {
		return false
	}
	md.addThetaLock("1", "2", "6")
	md.addThetaLock("1", "6", "2")
	return true
}

// registerNode2MirrorLocks couples nodes 3 and 7 on node 2's sphere via a bidirectional
// MIRROR lock: dragging either makes the other share its θ (angle from node 2's +y
// up-pole) AND take the opposite-sign φ (φ7 = −φ3), so the two stay on the same
// latitude ring around node 2, mirrored across the φ=0 meridian. Returns true when the
// lock was registered (all three nodes present), so the caller seeds it once at load
// (apply with node 3 as leader) to start 3 and 7 mirrored rather than only after the
// first drag.
func (md *MoveDispatch) registerNode2MirrorLocks(has func(string) bool) bool {
	if !(has("2") && has("3") && has("7")) {
		return false
	}
	md.addMirrorLock("2", "3", "7")
	md.addMirrorLock("2", "7", "3")
	return true
}

// registerChain567Locks registers the DIRECTIONAL meridian chain 6 → 5 → 7 (see
// lock.go phiDrive):
//   - phiZeroFollower(6,5): node 6 ANCHORS node 5. The lock writes only node 5
//     (project 5 onto 6's φ=0 meridian); node 6 is never moved by it. So dragging 6
//     pulls 5 along, dragging 5 re-projects 5 onto 6's meridian.
//   - phiZeroCenter(7,5): node 5 DRAGS node 7. The lock writes only node 7 (move 7 to
//     keep 5 on 7's φ=0 meridian); node 5 is never moved by it. So dragging 5 pulls 7
//     along; dragging 7 re-projects 7 (5 stays put).
//   - equalRadii(5,6,7): equalize the two edge radii into node 5 (|6→5| == |7→5|);
//     node 6 is the radius authority (anchor), node 7 is rescaled to it, folded into
//     node 7's φ=0 projection. Only when all three exist.
//
// Net chain: drag 6 → 5 follows → 7 follows; drag 5 → 7 follows (6 stays); drag 7 →
// neither 5 nor 6 moves (3 mirrors via 2↔7↔3). Returns (seedID, true) when registered:
// seed from the anchor (node 6) so node 5 is projected onto 6's meridian and node 7
// follows at the equalized radius; if node 6 is absent, seed by dragging node 5 (only
// the moveCenter lock is present).
func (md *MoveDispatch) registerChain567Locks(has func(string) bool) (string, bool) {
	if !(has("5") && (has("6") || has("7"))) {
		return "", false
	}
	if has("6") {
		md.addPhiZeroFollowerLock("6", "5") // 6 anchors 5
	}
	if has("7") {
		md.addPhiZeroCenterLock("7", "5") // 5 drags 7
	}
	if has("6") && has("7") {
		md.addEqualRadiiLock("5", "6", "7")
	}
	seedID := "6"
	if !has("6") {
		seedID = "5"
	}
	return seedID, true
}
