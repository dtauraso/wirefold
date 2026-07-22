// quantized_move.go — the quantized double-link local-polar move math split out of
// node_move.go: pure move, no logic changes. Held-state snapshots (heldCenters/
// heldEdges), the fan-out to edges/partners on a re-propagated center, pole requantizing,
// the one-hop neighborSetC propagation, the owner-goroutine commit (commitNodeMoveLocal),
// RootMove (the decentralized drag entry), requantizeLocalPolars, and reachRFromPolar.
//
// RootMove's invariant is load-bearing and MUST stay prominent after this move: it runs
// ONCE PER POINTER-MOVE EVENT, not once per drag (memory/project_rootmove_is_per_pointer_move.md).

package Wiring

import (
	"fmt"
	"math"
)

// heldCenters returns the gesture goroutine's own accumulated node-center map
// (md.positions — see its doc comment). GESTURE-GOROUTINE ONLY: every live caller
// (gesture.go) runs on the stdin/gesture dispatch loop, the same goroutine
// drainPositions fills md.positions from.
func (md *MoveDispatch) heldCenters() map[string]vec3 {
	return md.positions
}

func (md *MoveDispatch) heldEdges() []sphereEdge {
	edges := make([]sphereEdge, 0, len(md.edgeMovers))
	for _, em := range md.edgeMovers {
		edges = append(edges, sphereEdge{Source: em.srcID, Target: em.dstID})
	}
	return edges
}

// fanEdgesAndPartners messages every incident edge's mover (batched per-edge Centers) and
// every aimed-port partner (pure re-emit), for the given already-applied set of moved node
// centers. It never writes the moved node's OWN snap — that responsibility belongs to
// whichever caller applied the moved node's own center via applyCenter directly (every
// live caller is owner-goroutine; the old central "self-send into own inbox" path,
// fanCenters, was removed — it deadlocked/staled when its only caller turned out to run
// on the moved node's own goroutine too. See commitNodeMoveLocal for the applyCenter +
// fanEdgesAndPartners pattern).
func (md *MoveDispatch) fanEdgesAndPartners(newCenters map[string]vec3, enqueue func(id string, msg moveMsg)) {
	// Per-edge: send ONE batched message carrying every moved endpoint of that edge,
	// so an edge whose both endpoints moved this frame recomputes/emits exactly once.
	// enqueue (the sending node's own retry queue — see enqueueFuncFor) appends the
	// message to nm.pending and attempts an immediate non-blocking send on the
	// destination's own directed channel (extIn or the sender's slot in the
	// destination's neighborIn map), retrying next cycle if that channel isn't ready
	// to receive; it never blocks the calling handler goroutine, so this call — made
	// from inside handle via commitLocal — never blocks. Dispatch-existence (does id
	// resolve to a live mover) is checked at send time inside that retry path, matching
	// enqueue's other call sites (m.sendMove), which already tap/enqueue unconditionally
	// regardless of whether id resolves.
	for edgeID, em := range md.edgeMovers {
		eps := map[string]vec3{}
		if c, ok := newCenters[em.srcID]; ok {
			eps[em.srcID] = c
		}
		if c, ok := newCenters[em.dstID]; ok {
			eps[em.dstID] = c
		}
		if len(eps) == 0 {
			continue
		}
		enqueue(edgeID, moveMsg{Kind: moveMsgKindCenters, Centers: eps})
	}

	// Aimed-port re-emit (see doc comment above): find every partner node — the OTHER
	// end of any edge incident to a moved node — and ask it to re-emit its OWN geometry
	// with its OWN (unchanged) center, mirroring reemitPortTorusGeometry's "same center"
	// trick. emitGeometry reads m.partnerCenter at emit time, which reads the partner's
	// OWN neighborCenters cache for the moved node — kept fresh by the PartnerCenter
	// field below, carried on this SAME message (updated before the re-emit, in
	// nodeMover.handle) — so the partner's aimed port marker picks up the new target
	// direction. This does NOT run for torus-locked ports only — it runs for every
	// aimed connected port unconditionally, even if that breaks a port∈torus lock;
	// that is intended. partners maps partnerID → the ONE moved node it should cache
	// (only ever one moved id per call in production; a hypothetical multi-id batch
	// would have the last-seen moved neighbor win, matching newCenters' own map
	// iteration order having no other consumer of this ambiguity today).
	partners := map[string]string{}
	for _, em := range md.edgeMovers {
		if _, moved := newCenters[em.srcID]; moved {
			if _, alsoMoved := newCenters[em.dstID]; !alsoMoved {
				partners[em.dstID] = em.srcID
			}
		}
		if _, moved := newCenters[em.dstID]; moved {
			if _, alsoMoved := newCenters[em.srcID]; !alsoMoved {
				partners[em.srcID] = em.dstID
			}
		}
	}
	for partnerID, movedID := range partners {
		if _, ok := md.nodeMovers[partnerID]; !ok {
			continue
		}
		// Center is deliberately nil (see the doc comment above): this is a PURE
		// re-emit, not a position write for partnerID itself — a non-nil Center here
		// would be read by nodeMover.handle as "this is YOUR OWN new center" and
		// wrongly move partnerID. PartnerCenter/SenderID instead carry movedID's own
		// fresh center purely to update partnerID's neighborCenters[movedID] cache
		// (moveMsg.PartnerCenter's doc comment) before the re-emit — never applied as
		// partnerID's own position. nodeMover.handle's nil-Center branch re-emits from
		// the mover's own live geom, so it can never race or clobber a pending
		// position write. Per-target FIFO order (each sender's own retry queue drains
		// in append order onto that target's one directed channel) preserves ordering
		// now that delivery goes through the sender's own nm.pending/flushPending
		// instead of a shared outbox.
		movedCenter := newCenters[movedID]
		enqueue(partnerID, moveMsg{Kind: moveMsgKindCenter, NodeID: partnerID, Center: nil,
			SenderID: movedID, PartnerCenter: &movedCenter})
	}
}

// requantizePoleTraced is the SINGLE site every LOCAL-polar write routes through once a
// node's LayoutHolder exists (this file's several call sites). `updates` carries the FRESH
// offset (vec3, THIS node — nodeID — as origin) for each neighbor whose distance/direction
// just changed — the legitimate cart↔polar boundary entry (dirFromOffset + azimuthFrom).
//
// Every OTHER neighbor already on lh (unchanged this call) is NEVER re-measured against a
// live cartesian center: its direction is RECONSTRUCTED from its own stored indices about
// the OLD pole (lh.Pole(), persisted from the last call) via fromAxisFrame — arithmetic on
// stored ints × step constants, then one boundary trig call to turn that direction back into
// a vector. This is the fixed-increment/stored-index model
// (memory/feedback_abc_times_constant_not_rederive.md,
// docs/demos/polar-drag-3d.html's autoPole/ΔR⁻¹·q block): an unchanged neighbor's world
// position hasn't moved, so its stored indices ARE ground truth and are carried forward,
// adjusted only by the fixed pole increment (rotating_pole.go) when the measurement pole
// tilts. `pole = localPole(dirs)` is recomputed from the WHOLE neighbor set's directions
// (fresh from cartesian, unchanged from stored-index reconstruction) and then PERSISTED on
// lh (SetPole) so the next call's unchanged neighbors reconstruct against the pole THIS
// call actually quantized against.
//
// When the pole doesn't move (the common case — home stays home), an unchanged neighbor's
// re-expressed indices are byte-identical to what's already stored (fromAxisFrame then
// azimuthFrom about the SAME pole is an exact round-trip): the write is skipped, a true
// no-op, not a reproject that happens to land on the same numbers.
func (md *MoveDispatch) requantizePoleTraced(lh *LayoutHolder, updates map[string]vec3) dir {
	existing := lh.LocalPolarsSnapshot()
	oldPole := lh.Pole()

	existingByID := make(map[string]LocalPolar, len(existing))
	for _, lp := range existing {
		existingByID[lp.To] = lp
	}

	// Each neighbor's DIRECTION: fresh neighbors from their live cartesian offset (the
	// legitimate boundary entry); unchanged neighbors reconstructed from stored indices
	// about the OLD pole — no md.centerOfNode call for an unchanged neighbor.
	dirs := make(map[string]dir, len(existing)+len(updates))
	freshRadius := make(map[string]float64, len(updates))
	for id, o := range updates {
		d, r := dirFromOffset(o)
		dirs[id] = d
		freshRadius[id] = r
	}
	for _, lp := range existing {
		if _, fresh := updates[lp.To]; fresh {
			continue
		}
		t, p, _ := lp.effectiveSteps()
		dirs[lp.To] = fromAxisFrame(oldPole, float64(lp.QuantITheta)*t, float64(lp.QuantIPhi)*p)
	}

	dirVecs := make([]vec3, 0, len(dirs))
	for _, d := range dirs {
		dirVecs = append(dirVecs, dirToVec3(d))
	}
	newPole := localPole(dirVecs)

	for id, d := range dirs {
		t, p, rStep := lh.localPolarSteps(id)
		c, psi := azimuthFrom(newPole, d)
		iTheta := int(math.Round(c / t))
		iPhi := int(math.Round(psi / p))

		old, hadEntry := existingByID[id]
		_, fresh := updates[id]

		iR := old.QuantIR
		if fresh || !hadEntry {
			iR = int(math.Round(freshRadius[id] / rStep))
		}

		if !fresh && hadEntry &&
			old.QuantITheta == iTheta && old.QuantIPhi == iPhi && old.QuantIR == iR &&
			old.StepTheta == t && old.StepPhi == p && old.StepR == rStep {
			continue // true no-op: pole/indices unchanged, skip the write
		}
		lh.SetLocalPolar(id, iTheta, iPhi, iR, t, p, rStep)
	}
	lh.SetPole(newPole)
	return newPole
}

// neighborSetCRequantize is the OWNER-GOROUTINE half of a neighbor's edge re-quantize
// (moveMsgKindNeighborSetC): the dragged node fromID moved, so selfID's stored local
// polar to fromID no longer matches the live geometry. selfID STAYS PUT — dragging fromID
// moves only fromID — and re-quantizes its OWN edge to fromID from the live offset, with
// theta, phi AND r all fresh (about selfID's rotating pole, via requantizePoleTraced with
// fromID as the single fresh update); selfID's OTHER neighbors are carried forward as
// index x step, not re-derived. There is NO reposition: only fromID moved, so the incident
// fromID-selfID edge redraws from fromID's own commit (fanEdgesAndPartners on fromID's
// side). Single hop — no forward past selfID, no cascade. No-op for an unknown selfID.
// deltaA/deltaB/deltaC are the DRAGGED node fromID's OWN quantized-triple change for its
// edge to selfID (computed once on fromID's goroutine, see requantizeLocalPolars) —
// pure observability payload carried through to the AbcDrag trace event, never applied
// to selfID's own position/quantize math. selfCenter is selfID's OWN current center —
// read by the caller (nodeMover.handle, on selfID's own goroutine, nodeWorldPos(m.geom))
// rather than by an atomic cross-goroutine snap read.
func (md *MoveDispatch) neighborSetCRequantize(selfID, fromID string, selfCenter, fromCenter vec3, deltaA, deltaB, deltaC int) {
	lh, ok := md.layoutHolders[selfID]
	if !ok {
		return
	}
	// Offset convention matches requantizeLocalPolars: neighbor(fromID) center - self
	// center. fromID is the ONLY fresh update, so requantizePoleTraced re-derives selfID's
	// edge to fromID (theta, phi AND r, about selfID's rotating pole) at the cart<->polar
	// boundary, while every OTHER neighbor of selfID is carried forward as index x step.
	md.requantizePoleTraced(lh, map[string]vec3{fromID: fromCenter.sub(selfCenter)})

	// EVERY node that receives an abc change from a dragged peer logs its response so the
	// drag propagation is observable (probe-merge.sh --debug -> .probe/go-debug.jsonl) and
	// the in-editor overlay log can list all recipients — NOT gated to time nodes: any node
	// that gets the message (gate, time, pulse, ...) is a recipient and must be mentioned.
	// Behavior is still the plain stay-put re-quantize above; this is the observability
	// step, not a motion/propagation change. The logged abc is selfID's freshly re-quantized
	// edge to the peer.
	if md.tr != nil {
		var it, ip, ir int
		for _, lp := range lh.LocalPolarsSnapshot() {
			if lp.To == fromID {
				it, ip, ir = lp.QuantITheta, lp.QuantIPhi, lp.QuantIR
				break
			}
		}
		md.tr.Breadcrumb("abc-drag", selfID, fromID,
			fmt.Sprintf("peer=%s peerCenter=(%.3f,%.3f,%.3f) abc=(%d,%d,%d) delta=(%d,%d,%d)",
				fromID, fromCenter.X, fromCenter.Y, fromCenter.Z, it, ip, ir, deltaA, deltaB, deltaC))
		// Routed counterpart of the breadcrumb above: marks selfID in the buffer so the
		// in-editor overlay log lists it (the breadcrumb alone never reaches the buffer).
		// deltaA/deltaB/deltaC ride along so the in-editor drag-log can show what selfID
		// received.
		md.tr.AbcDrag(selfID, deltaA, deltaB, deltaC)
	}

	if md.persist.quantOffset != nil {
		if root := md.persist.quantOffset.root; root != "" {
			if err := WriteLocalPolars(root, selfID, lh.LocalPolarsSnapshot(), lh.Pole()); err != nil {
				logPersistErr("local_polar_persist", selfID, err)
			}
		}
	}
}

// commitNodeMoveLocal is the OWNER-GOROUTINE single-node commit path
// (generalized to every node): used when the commit
// originates on nodeID's OWN mover goroutine (its own inbox handler for a
// moveMsgKindDrag). It applies nodeID's OWN new center SYNCHRONOUSLY via
// applyCenter — safe and correct here because applyCenter's doc contract is "called
// only from this nodeMover's own inbox-drain goroutine", which this is. Also fans
// centers to incident edges/partners, persists the per-node quantized-offset
// (nodeMover.quantOffset — never a shared map, so no other mover goroutine's commit
// can race this write even for a different node id), and requantizes nodeID's
// local-polar double-links against its (unmoved) neighbors.
func (md *MoveDispatch) commitNodeMoveLocal(nodeID string, newPos vec3) {
	edges := md.heldEdges()
	nm, ok := md.nodeMovers[nodeID]
	// reach[nodeID] only ever needs nodeID's own fresh polar plus its DIRECT
	// neighbors' polar (reachRFromPolar only accumulates reach for an edge's
	// SOURCE, from that edge's Target) — nm.neighborCenters (this node's own
	// goroutine-local cache, no cross-goroutine read) already holds every direct
	// neighbor's last-reported CARTESIAN center; scene polar is a pure re-derive off
	// the fixed, write-once md.sceneSphere.Center (never mutated after load), so this
	// stays race-free without the old all-nodes atomic-snapshot read (heldPolar).
	polars := map[string]polar{}
	if ok {
		for id, c := range nm.neighborCenters {
			polars[id] = cart2polar(c.sub(md.sceneSphere.Center))
		}
	}
	// Single cart2polar boundary conversion for this drag target — newPos is mouse-
	// derived cartesian (gesture.go ray/plane unproject); everything downstream
	// (reach, measureScalar, the persist schedule) reuses this one polar value rather
	// than re-deriving it from newPos.
	nodePolar := cart2polar(newPos.sub(md.sceneSphere.Center))
	polars[nodeID] = nodePolar
	reach := reachRFromPolar(polars, edges)

	if ok {
		nm.applyCenter(newPos, reach[nodeID])
	}
	md.fanEdgesAndPartners(map[string]vec3{nodeID: newPos}, md.enqueueFuncFor(nodeID))

	if md.quantizedLayout && ok {
		off := measureScalar(nodePolar, nm.quantOffset)
		nm.quantOffset = off
		if md.persist.quantOffset != nil {
			md.persist.quantOffset.schedule(nodeID, off, nodePolar)
		}
	}

	md.requantizeLocalPolars(nodeID, newPos)
}

// RootMove handles a node-drag under the flat absolute scene-polar layout: every node
// is positioned independently about the scene sphere center — there is no reference/
// parent concept, so dragging moves ONLY the dragged node (no cascade). The dragged
// node's new world position is the drag target itself — CONTINUOUS, not snapped to any
// grid (double-link local-polar model: the node's position is free; only each
// neighbor's DISTANCE to it is quantized, each on that neighbor's own small grid — see
// requantizeLocalPolars).
//
// RootMove is the decentralized drag entry, widened to EVERY node (the generalization
// that came with the quantizedOffsets data-race fix): dragging any node does not commit
// on the stdin reader's own goroutine — it routes a single moveMsgKindDrag to the
// dragged node's OWN inbox and returns. The dragged node's own moveMsgKindDrag handler
// (nodeMover.handle) does the rest, entirely on its own goroutine: commit its own new
// position (commitLocal — fan + persist + requantize, no cross-goroutine self-send).
// commitLocal's requantizeLocalPolars then sends every direct domain neighbor a single
// moveMsgKindNeighborSetC assignment (see that function's doc comment) — there is no
// equal-radii solve, no rule-node cascade, no gate-anchor fan-out; a drag never touches
// any node's position but its own. Returns false for an unknown node.
//
// NOTE: RootMove runs ONCE PER POINTER-MOVE EVENT, not once per drag (two bugs — commits
// 338f05da, 154a05bd — came from assuming otherwise; see
// memory/project_rootmove_is_per_pointer_move.md). The drag-log reset is NOT emitted
// here for that reason: the reset belongs at the real drag-start edge (the
// pending→dragging transition in gesture.go), not on every move tick RootMove sees.
func (md *MoveDispatch) RootMove(nodeID string, target vec3) bool {
	if _, ok := md.nodeMovers[nodeID]; !ok {
		return false
	}
	// Route the drag itself to the dragged node's OWN inbox instead of committing on
	// the stdin reader's goroutine — every node's moveMsgKindDrag handler commits
	// (synchronous local apply, reported over reportCh) on its own goroutine. No
	// central commit call here.
	md.sendMove(nodeID, moveMsg{Kind: moveMsgKindDrag, NodeID: nodeID, Target: target})
	return true
}

// requantizeLocalPolars implements the double-link local-polar model on a drag: the
// dragged node X's new position gives each of its domain neighbors M a NEW distance to
// it. That distance is quantized to a whole tick on THAT neighbor's own small grid
// (layout_holder.go localStepTheta/localStepPhi/localStepR, or M's stored per-neighbor
// step constants) — and likewise X's own local polar TO M is requantized on X's own
// grid. The two ends' quantized values are independent and never reconciled or
// reconstructed from one another (MODEL.md "no blow-up, by construction" — this is the
// local-polar analogue: nothing rebuilds X's position from a local polar). Both ends'
// LayoutHolders are updated in memory and persisted.
//
// Decentralized (mirrors nodeMover.quantOffset): X requantizes+persists its OWN holder
// synchronously here, on its own (the mover's) goroutine — the single-writer case. Each
// domain neighbor M's own holder is written only by M's own goroutine: X sends M a
// single moveMsgKindNeighborSetC assignment (X's fresh newPos as FromCenter, X's newly
// requantized c to M as SnapC) instead of reaching into M's LayoutHolder directly, so a
// holder is mutated only by its own node's goroutine, exactly like quantOffset. M keeps
// its own stored bearing to X and repositions itself at the new distance along it (see
// neighborSetCReposition) — unconditional for every neighbor.
func (md *MoveDispatch) requantizeLocalPolars(nodeID string, newPos vec3) {
	lhX, okX := md.layoutHolders[nodeID]
	if !okX {
		return
	}
	neighbors := map[string]bool{}
	for _, em := range md.edgeMovers {
		if em.srcID == nodeID {
			neighbors[em.dstID] = true
		} else if em.dstID == nodeID {
			neighbors[em.srcID] = true
		}
	}
	if len(neighbors) == 0 {
		return
	}
	root := ""
	if md.persist.quantOffset != nil {
		root = md.persist.quantOffset.root
	}
	writePersist := func(id string, holder *LayoutHolder) {
		if root == "" {
			return
		}
		if err := WriteLocalPolars(root, id, holder.LocalPolarsSnapshot(), holder.Pole()); err != nil {
			logPersistErr("local_polar_persist", id, err)
		}
	}

	// X's local polars TO every reachable neighbor, resolved about X's rotating local
	// pole (rotating_pole.go) in ONE pass — the pole must see the WHOLE neighbor set, not
	// just one at a time, so a kick from one offset is checked against every other. cM
	// comes off X's OWN nodeMover.neighborCenters cache (this node's own goroutine,
	// updated only by this same goroutine's handle — no cross-goroutine read), not a
	// cross-goroutine atomic snap read of M's mover.
	nmX := md.nodeMovers[nodeID]
	updatesX := map[string]vec3{}
	if nmX != nil {
		for m := range neighbors {
			cM, ok := nmX.neighborCenters[m]
			if !ok {
				continue
			}
			updatesX[m] = cM.sub(newPos)
		}
	}
	if len(updatesX) == 0 {
		return
	}
	// oldByTo is X's DRAG-ANCHORED triple to each neighbor — the triple X had at the
	// START of the CURRENT drag, not at the previous move event. RootMove runs on every
	// ~8ms pointer-move, far finer than one quantize step (round(angle/step) lands on
	// the same integer for dozens of consecutive frames), so a per-move-event delta was
	// almost always (0,0,0) even mid-drag. The anchor is armed once, at the real
	// drag-start edge (gesture.go's gestPending->gestDragging transition sends
	// moveMsgKindDragStart to nodeID's own inbox, handled by armDragAnchor on this same
	// goroutine) so it accumulates across the whole drag and reads the drag's true total
	// on release. If no dragStart ever armed it (a programmatic RootMove with no
	// gesture, as several tests do), lazy-arm it right here from X's CURRENT
	// (pre-requantize) triple, so that first commit's delta is (0,0,0) and every later
	// commit in the same unarmed "drag" is relative to it — never a stale anchor from a
	// previous drag, since armDragAnchor always overwrites. Computed once, on X's own
	// goroutine — per CLAUDE.md's model (each goroutine reports what it itself picked
	// up). Pure observability: it does not gate or alter the requantize below in any way.
	if nmX != nil && !nmX.dragAnchorArmed {
		nmX.armDragAnchor()
	}
	oldByTo := map[string]LocalPolar{}
	if nmX != nil {
		oldByTo = nmX.dragAnchorByTo
	}
	md.requantizePoleTraced(lhX, updatesX)
	writePersist(nodeID, lhX)

	// X tells EVERY direct domain neighbor M its NEW c (the quantized edge radius X just
	// requantized to M above) as a SINGLE ASSIGNMENT — moveMsgKindNeighborSetC. M keeps
	// its OWN stored bearing (QuantITheta/QuantIPhi) to X and repositions itself at the
	// new distance along that same stored direction, X held fixed; M does NOT
	// re-derive its bearing from a live offset and does NOT forward beyond this one
	// hop (neighborSetCReposition). Routed as a message on M's own directed inbound
	// channel (M's neighborIn[X] slot) instead of reaching into M's LayoutHolder from
	// X's (this) goroutine — each M's holder and center are written only by M's own
	// goroutine. Sent via X's OWN retry queue (md.enqueueFuncFor(nodeID), the same
	// handle every other fan in this commit path uses — see commitNodeMoveLocal's
	// fanEdgesAndPartners call above) instead of the direct-to-inbox sendMoveLossy
	// this used before: measured under the same mutually-adjacent concurrent-drag
	// flood TestMutuallyAdjacentDragFloodNoDeadlock drives, sendMoveLossy dropped
	// ~98% of NeighborSetC sends (9417/9600 in one run, TestNeighborSetCDropReachability)
	// — the "drop is safe, self-heals" justification was true in the sense that
	// nothing deadlocked, but the drop path was not a rare backstop, it was the
	// common case, silently discarding almost every NeighborSetC. Routing through
	// nodeID's own retry queue instead gets the SAME deadlock-safety property
	// sendMoveLossy was reaching for (decouples the send from this handler
	// goroutine, so two mutually-adjacent nodes committing concurrently can't block
	// each other) but via non-blocking send-and-retain-on-nm.pending, retried every
	// cycle of the sender's own clock-paced run loop, instead of a drop. Unconditional
	// for every neighbor — there is no rule/gate/anchor cascade left to defer to.
	enqueue := md.enqueueFuncFor(nodeID)
	lpByTo := map[string]LocalPolar{}
	for _, lp := range lhX.LocalPolarsSnapshot() {
		lpByTo[lp.To] = lp
	}
	for m := range updatesX {
		newLP, ok := lpByTo[m]
		if !ok {
			continue
		}
		// X's own triple-change for its edge to m: new - old, or (0,0,0) if X had no
		// prior stored triple to m (nothing to subtract from — see requantizeLocalPolars
		// doc). Computed once here, on X's own goroutine, and carried unmodified to
		// EVERY recipient — not recomputed per-recipient.
		var deltaA, deltaB, deltaC int
		if oldLP, hadOld := oldByTo[m]; hadOld {
			deltaA = newLP.QuantITheta - oldLP.QuantITheta
			deltaB = newLP.QuantIPhi - oldLP.QuantIPhi
			deltaC = newLP.QuantIR - oldLP.QuantIR
		}
		enqueue(m, moveMsg{Kind: moveMsgKindNeighborSetC, NodeID: m, SenderID: nodeID, FromCenter: newPos,
			DeltaA: deltaA, DeltaB: deltaB, DeltaC: deltaC})
	}
}

// reachRFromPolar computes each node's sphere REACH radius (max distance from a node to any
// node it outputs to) under the given polar positions and edge set. Distance is the spherical
// law-of-cosines distance between the two polar positions (polarDist) — no cartesian, no vector
// subtraction. Called by loader.go buildFromSpec and by RootMove so the fanned "center" message
// carries the new reach radius and the ring stays sized during a drag.
func reachRFromPolar(polars map[string]polar, edges []sphereEdge) map[string]float64 {
	reachR := map[string]float64{}
	for _, e := range edges {
		sp, okS := polars[e.Source]
		tp, okT := polars[e.Target]
		if !okS || !okT {
			continue
		}
		if d := polarDist(sp, tp); d > reachR[e.Source] {
			reachR[e.Source] = d
		}
	}
	return reachR
}
