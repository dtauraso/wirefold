package Wiring

// lock_registry.go — per-node registration of the polar layout locks
// (docs/planning/visual-editor/polar-coordinate-model.md §7). Each lock is a
// RELATIONSHIP defined on some node's frame; this file groups the registrations so
// the locks a given node ORIGINATES are findable in one place (grep the node id or
// read its register* method) rather than buried in loader.go's startup sequence.
//
// Discoverability convention: a lock is filed under the node whose LOCAL FRAME it is
// defined on — the Center for mirror locks, the meridian subject for the φ=0 chain.
// So "node 2's locks" = the locks centered on node 2's sphere (the 3/7 mirror).
//
// Locks are REGISTERED here and applied only on a drag (applyLocks, via the per-node
// move handlers). There is no load-time seeding: saved node positions stand as loaded.
// Registration order still matters for the applyLocks BFS move-once guard, so loader.go
// keeps the node-2 mirror ahead of the 5/6/7 chain (node 7's equal-radii fold composes
// the same way it does on a live drag).

// registerNode2MirrorLocks couples nodes 3 and 7 on node 2's sphere via a bidirectional
// MIRROR lock: dragging either makes the other share its θ (angle from node 2's +y
// up-pole) AND take the opposite-sign φ (φ7 = −φ3), so the two stay on the same
// latitude ring around node 2, mirrored across the φ=0 meridian. Returns true when the
// lock was registered (all three nodes present); the return is currently unused (no
// load seeding) but kept for symmetry with the other registrars.
func (md *MoveDispatch) registerNode2MirrorLocks(has func(string) bool) bool {
	if !(has("2") && has("3") && has("7")) {
		return false
	}
	md.addMirrorLock("2", "3", "7")
	md.addMirrorLock("2", "7", "3")
	return true
}

// registerNode9MirrorLocks couples nodes 6 and 2 on node 9's sphere via a bidirectional
// MIRROR lock. Node 9 is a structural clone of node 2 one level up: child of node 1
// (1→9), parent of 6 and 2 (9→6, 9→2), mirroring its two children just as node 2
// mirrors 3 and 7. Node 6 is the leader; node 2 takes shared θ (about node 9's +y
// up-pole) and opposite-sign φ.
//
// This mirror is the sole coupling between nodes 2 and 6. When the node-3 authority
// flip places node 6 (following node 7's radius), the BFS fires mirror(9,6,2) so
// node 2 follows node 6 — the same role the now-deleted node-1 theta lock used to play.
func (md *MoveDispatch) registerNode9MirrorLocks(has func(string) bool) bool {
	if !(has("9") && has("6") && has("2")) {
		return false
	}
	md.addMirrorLock("9", "6", "2")
	md.addMirrorLock("9", "2", "6")
	return true
}

// registerNode10Locks gives node 10 (a Pulse, same type as node 6) the SAME lock
// system node 6 has, with every partner renamed. Node 6 plays two roles, reproduced
// here under the mapping {6→10, parent 9→1, mirror sibling 2→11, chain mid 5→12,
// chain other 7→13}:
//   - mirror child of its parent: node 6 is mirror-paired with node 2 about node 9
//     → node 10 is mirror-paired with node 11 about node 1 (its feeder).
//   - chain anchor via its OUTPUT: node 6 anchors node 5 (phiZeroFollower) and is the
//     equal-radii authority for node 7 about node 5 → node 10 anchors node 12 and is
//     the authority for node 13 about node 12.
//
// Nodes 11/12/13 do not exist yet (node 10's Out port is unwired), so every call below
// is guarded out by has() and the locks stay DORMANT — they activate name-for-name once
// those partner nodes are added. This is the intended "a few locks won't exist yet".
// Rename 11/12/13 here when the real partner nodes are created.
func (md *MoveDispatch) registerNode10Locks(has func(string) bool) {
	// Mirror: node 10 ↔ node 11 about node 1 (parallels mirror(9,6,2)/(9,2,6)).
	if has("1") && has("10") && has("11") {
		md.addMirrorLock("1", "10", "11")
		md.addMirrorLock("1", "11", "10")
	}
	// Chain anchor: node 10 anchors node 12 (parallels phiZeroFollower(6,5)).
	if has("10") && has("12") {
		md.addPhiZeroFollowerLock("10", "12")
	}
	// Equal radii: node 10 is the authority for node 13 about node 12
	// (parallels equalRadii(5,6,7)).
	if has("12") && has("10") && has("13") {
		md.addEqualRadiiLock("12", "10", "13")
	}
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
// neither 5 nor 6 moves (3 mirrors via 2↔7↔3). Returns (anchorID, true) when registered
// — the anchor that WOULD seed the chain (node 6, or node 5 when 6 is absent). The
// return is currently unused (no load seeding) but kept for when a seed origin is needed.
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
