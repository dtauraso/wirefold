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

// registerNode1MirrorLocks couples node 1's two chain-head children, node 9 and node
// 10, via a bidirectional MIRROR (rotation) lock about node 1 — the same rotation
// coupling node 9 gives its children 6 and 2, one level up. Dragging either of 9/10
// makes the other share its θ about node 1's +y up-pole and take the opposite-sign φ,
// so the pair rotates together about node 1.
func (md *MoveDispatch) registerNode1MirrorLocks(has func(string) bool) bool {
	if !(has("1") && has("9") && has("10")) {
		return false
	}
	md.addMirrorLock("1", "9", "10")
	md.addMirrorLock("1", "10", "9")
	return true
}

// registerBisector11Locks constrains node 11 (the WindowAndInhibitLeftGate, the mid)
// to the perpendicular-bisector plane of its two feeders, node 10 and node 6, so the
// incoming branch radii stay equal (|10→11| == |6→11|). Node 10 and node 6 are FREE;
// node 11 follows. This is the second-layer analog of registerBisector5Locks and
// replaces the old registerChain10_11_6Locks φ=0 chain.
func (md *MoveDispatch) registerBisector11Locks(has func(string) bool) {
	if !(has("11") && has("10") && has("6")) {
		return
	}
	md.addBisectorMidLock("11", "10", "6")
}

// registerBisector5Locks constrains node 5 (the gate/mid) to the perpendicular-bisector
// plane of its two feeders, node 6 and node 7, so the two incoming branch radii stay
// equal (|6→5| == |7→5|). Node 6 and node 7 are FREE — the user drags them; node 5
// follows. This replaces the old directional 6→5→7 φ=0 chain (where node 5 was the
// fixed frame and node 7 was rescaled).
func (md *MoveDispatch) registerBisector5Locks(has func(string) bool) {
	if !(has("5") && has("6") && has("7")) {
		return
	}
	md.addBisectorMidLock("5", "6", "7")
}
