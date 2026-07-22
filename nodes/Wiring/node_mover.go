// node_mover.go — the per-node/per-edge mover actor types split out of node_move.go. Pure
// move: no logic changes beyond the two-channels-no-inbox-no-blocking restructure:
// there is no shared many-to-one inbox anymore. Every pair of movers that talk gets its OWN dedicated directed channel
// (nodeMover.neighborIn, edgeMover.srcIn/dstIn), plus one dedicated "external" channel per
// mover (extIn) for the stdin/gesture goroutine's rare direct entries (drag/dragStart/
// anchor). node_move.go retains the dispatch registry (MoveDispatch) that WIRES these
// channels together at load time; this file owns the actors themselves — nodeMover (owns
// one node's geometry, its own inbound channel set, and its own outbound retry queue) and
// edgeMover (owns one edge's segment/arc + in-flight bead revision). Each mover touches
// MoveDispatch only via an injected enqueue func — no back-reference to the dispatch
// registry, and no shared queue/lock between movers.

package Wiring

import (
	"context"
	"fmt"

	T "github.com/dtauraso/wirefold/Trace"
)

// pendingSend is one (destination, message) pair this node's own goroutine tried to
// deliver, failed (the target's inbox was momentarily full), and is retrying — see
// nodeMover.pending's doc comment. There is no separate sender goroutine and no lock:
// only nm's own goroutine ever reads or writes nm.pending.
type pendingSend struct {
	destID string
	msg    moveMsg
}

// setPortAnchorId sets the AnchorId on the named port within the given geom,
// clearing any free-direction Anchor so AnchorId takes highest priority (matching
// portDir's resolution order: AnchorId > Anchor > side/slot). Returns true if the
// port was found and mutated. The geom is mutated in place (its slice elements are
// addressable). Used by both movers to apply a snapped ring-anchor update.
func setPortAnchorId(g *nodeGeom, port string, isInput bool, anchorId int) bool {
	list := g.Outputs
	if isInput {
		list = g.Inputs
	}
	for i := range list {
		if list[i].Name == port {
			list[i].AnchorId = &anchorId
			return true
		}
	}
	return false
}

// nodeMover owns one node's geometry. It drains its own dedicated inbound channels
// (extIn + one per neighbor — there is no single shared inbox) in its own goroutine
// and, on a move for itself, updates its held position and re-emits its node-geometry.
type nodeMover struct {
	id   string
	geom nodeGeom
	// extIn is this node's dedicated channel for EXTERNAL entries — the stdin/gesture
	// goroutine's drag/dragStart/anchor sends (md.sendMove, gesture.go's
	// applyRingAnchor). Nothing else ever writes here: no other mover shares it.
	extIn chan moveMsg
	// neighborIn holds one dedicated inbound channel PER ADJACENT NODE (keyed by that
	// neighbor's id) — the "two channels, A→B and B→A" topology generalized to this
	// node's whole neighbor set. Built once at construction (newMoveDispatch) from edge
	// adjacency and never mutated afterward, so it's safe for run() to snapshot into a
	// fixed select-case list at goroutine start. A neighbor M's own goroutine is the only
	// writer of neighborIn[M]; nothing else ever sends on it.
	neighborIn map[string]chan moveMsg
	tr         *T.Trace
	// clockSrc is the Clock this nodeMover's own goroutine (run) Copies from EXACTLY
	// ONCE at its own start, into
	// clk below — the same pattern edgeMover.run and DriveHeld already use, so the
	// mover is no longer the odd one out pacing on a bare wall-clock timer. Not read
	// again after that copy.
	clockSrc Clock
	// clk is this nodeMover's OWN clock copy, set once by run() at goroutine start.
	// Only this goroutine ever reads it. Defaults to a fresh, real, live-ticking
	// RealClock (see newNodeMover) so a test that never launches run() (e.g. a bare
	// nodeMover literal driving flushPending directly) never dereferences a nil Clock.
	clk Clock
	// speedCh delivers a speed change to THIS nodeMover's own clk copy
	// (per-goroutine-clock.md "Delivery"), polled via ApplySpeedNonBlocking every
	// cycle of run's loop. Set once, at construction (newMoveDispatch), from the
	// loader's build-wide speed-sink accumulator; nil in bare test construction, which
	// is fine — ApplySpeedNonBlocking is a no-op on a nil channel.
	speedCh chan float64
	// There is no geomMu. m.geom (port_geometry.go) splits into an embedded, write-once
	// nodeIdentity (Kind/Label/R/SceneCenter — set once at construction in loader.go,
	// grepped clean of any later write anywhere in this package) and MUTABLE state
	// (ScenePolar/HasPos/ReachR/Inputs/Outputs-element-AnchorId) written only by
	// applyCenter and handle's moveMsgKindAnchor case. Every writer AND every reader of
	// the mutable part — applyCenter, setPortAnchorId (via handle), emitGeometry's
	// full-struct copy — runs exclusively on nodeMover's OWN inbox-drain goroutine
	// (run/handle), so there is never more than one goroutine touching that memory. The
	// one cross-goroutine reader, MoveDispatch.NodeKind (node_move.go), called from the
	// gesture/stdin-reader goroutine, reads ONLY nm.geom.Kind — a field on the embedded
	// nodeIdentity, which no writer here ever touches. So the two properties that would
	// require a lock (a mutable field read cross-goroutine, or an identity field that
	// could gain a second writer) both provably don't hold, by construction of the type
	// split, not by coincidence of which byte ranges happen to overlap today.
	//
	// CHECKED BY CODE: TestNodeKindConcurrentWithApplyCenterUnderRace
	// (node_mover_geom_race_test.go) drives NodeKind's reader loop and applyCenter's
	// writer loop concurrently under -race with no lock on either side, as a standing
	// regression check that the split holds (a future change reintroducing a write to an
	// identity field, or widening NodeKind's read to a whole-struct copy, would make it
	// fail). There is no separate per-node "Update()" writer goroutine — that was the
	// retired SLICE 3 architecture.
	// neighborCenters caches THIS node's own view of every DIRECT domain neighbor's
	// (edge-adjacent node's) last-reported world center — keyed by neighbor id. Written
	// ONLY by this node's own goroutine (handle: moveMsgKindNeighborSetC's FromCenter,
	// and the aimed-port pure-reemit's PartnerCenter field), read ONLY by this node's
	// own goroutine (partnerCenter at emit time, requantizeLocalPolars, and
	// commitNodeMoveLocal's reach computation) — no cross-goroutine access at all,
	// unlike the atomic-snapshot pattern this replaced (each mover reading ANOTHER
	// mover's published pointer). Seeded once at construction (newMoveDispatch) from
	// load-time geoms for every edge-adjacent neighbor, so a lookup before the first
	// move always has a valid (if eventually stale-until-first-report) center.
	neighborCenters map[string]vec3
	// reportCh is the SEND end of the movers→gesture position-report channel
	// (MoveDispatch.posReportCh) — the external-observer pattern portTblC/edgeTblC/
	// nodeTblC already use, generalized to node centers. applyCenter sends this node's
	// fresh {id, center} on it non-blockingly every time it commits a new center. nil in
	// bare test construction that skips newMoveDispatch; a nil-channel send inside a
	// select-with-default is a no-op, never a block.
	reportCh chan posReport
	// sendMove routes a moveMsg to another id's OWN dedicated channel (resolveDest, above)
	// — no shared inbox, no shared mutable state.
	// Bound to md.enqueueFor(nm): it appends to nm.pending and immediately attempts a
	// non-blocking flush (never blocks the calling handler goroutine).
	sendMove func(id string, msg moveMsg)
	edgeIDs  []string
	// commitLocal is the OWNER-GOROUTINE commit path, bound to
	// md.commitNodeMoveLocal (generalized to every node). It applies this node's own
	// new center SYNCHRONOUSLY via applyCenter instead of enqueuing an async self-send,
	// so it is safe to call from THIS node's own handle() for a moveMsgKindDrag, with
	// no cross-goroutine self-send and no shared mutable state (each node's quantized
	// offset lives on its own mover — see nodeMover.quantOffset). nil in tests that
	// build a bare nodeMover directly.
	commitLocal func(id string, newPos vec3)
	// partnerCenter resolves, per (port,isInput) on this node, the CURRENT world center of
	// the single partner node connected via one edge (aimed-port model, port_geometry.go
	// portWorldPosAimed / builders.go partnerCenterFn). Wired by newMoveDispatch to read
	// THIS node's own neighborCenters cache (above) — no cross-goroutine access. nil
	// only in tests that build a bare nodeMover directly.
	partnerCenter partnerCenterFn
	// quantOffset is THIS node's own quantized polar offset (iTheta,iPhi,iR + step
	// constants) about the scene center — the per-node replacement for the formerly
	// shared md.quantizedOffsets map, which one mover goroutine's read could race
	// another mover goroutine's write on the SAME Go map object even for different
	// keys (fatal "concurrent map read and map write"). Seeded at load
	// (buildMoveDispatch) from the computed/persisted offset, then mutated ONLY by
	// this node's own commit path (commitNodeMoveCommon, called from this node's own
	// goroutine via commitLocal) — single-writer, no map, no race.
	quantOffset quantizedOffset
	// neighborSetC runs THIS node's own plain-neighbor set-c redraw (keep stored
	// bearing, write only the new c, reposition self) — bound to
	// md.neighborSetCRequantize. Dispatched from moveMsgKindNeighborSetC so a domain
	// neighbor's holder AND world position are written only by that neighbor's OWN
	// goroutine. selfCenter is THIS node's own current center (nodeWorldPos(m.geom),
	// read on this node's own goroutine — replaces the old md.centerOfNode(selfID)
	// atomic read). nil in tests that build a bare nodeMover directly.
	neighborSetC func(selfID, fromID string, selfCenter, fromCenter vec3, deltaA, deltaB, deltaC int)
	// pending is THIS node's own outbound retry queue: sendMove appends here and attempts an immediate
	// non-blocking send; an item that can't be delivered right now (the target's
	// inbox is momentarily full) stays here and is retried — before any newer item to
	// the SAME destination — on the next flushPending call, which nm's own run loop
	// makes every cycle. There is no dedicated sender goroutine and no lock: only
	// nm's own goroutine ever touches nm.pending (every sendMove call originates from
	// nm.handle, which only ever runs on nm's own run-loop goroutine). This is the
	// same retain-and-retry shape PacedWire already uses for its outCh delivery
	// handoff (full → retry next cycle, bead stays in inflight) — reused rather than
	// a second invented pattern.
	pending []pendingSend
	// resolveDest looks up the ONE dedicated directed channel FROM this node TO the
	// given destination id — the destination's neighborIn[this node's id] if destID is
	// another node, or the destination edge's srcIn/dstIn depending on which endpoint
	// this node is (md.nodeMovers/md.edgeMovers are read-only directories after
	// construction, safe to read from any goroutine). There is no shared inbox to look
	// up: every (sender, destination) pair resolves to its OWN channel. nil only in
	// tests that build a bare nodeMover directly, in which case flushPending is a no-op.
	resolveDest func(id string) (chan moveMsg, bool)
	// layoutHolderFn resolves THIS node's own LocalPolar holder (md.layoutHolders[id])
	// at CALL TIME rather than caching the *LayoutHolder at nodeMover construction:
	// buildMoveDispatch (which constructs nodeMovers) runs BEFORE buildNodes (which is
	// what actually populates md.layoutHolders[id] on each node's embedded
	// LayoutHolder), so a value cached at construction would be permanently nil. The
	// map itself is a read-only directory once the whole load completes (same pattern
	// as dispatch/edgeIDs) — safe to read from any goroutine after that point. Read
	// here only by armDragAnchor, which runs exclusively on this node's own goroutine
	// (moveMsgKindDragStart, dispatched via handle).
	layoutHolderFn func() *LayoutHolder
	// dragAnchorByTo, dragAnchorArmed: THIS node's drag-anchor snapshot (see
	// moveMsgKindDragStart's doc comment) — the per-neighbor LocalPolar triples as of
	// the start of the CURRENT drag. Written only by armDragAnchor (moveMsgKindDragStart
	// handler) or by requantizeLocalPolars' lazy-arm fallback (first commit of a drag
	// that never got an explicit dragStart, e.g. a programmatic RootMove in a test) —
	// both run on this node's own goroutine. Cleared (dragAnchorArmed=false) by
	// armDragAnchor so a NEW drag on the same node always re-arms rather than reusing a
	// stale anchor from a previous drag.
	dragAnchorByTo  map[string]LocalPolar
	dragAnchorArmed bool
}

func newNodeMover(id string, geom nodeGeom, tr *T.Trace, clockSrc Clock) *nodeMover {
	// clk defaults to a fresh RealClock (its own independent origin — fine here: this
	// default is only ever read by a test that never launches run() as a goroutine;
	// production always overwrites it below with clockSrc.Copy() before the goroutine
	// does anything else), matching newEdgeMover's same default for the same reason.
	nm := &nodeMover{
		id: id, geom: geom,
		extIn: make(chan moveMsg, 8), neighborIn: map[string]chan moveMsg{}, tr: tr,
		clockSrc: clockSrc, clk: NewRealClock(),
		// neighborCenters starts empty here; newMoveDispatch seeds every
		// edge-adjacent neighbor's entry from load-time geoms once edge adjacency
		// is known (this constructor runs before edges are wired).
		neighborCenters: map[string]vec3{},
	}
	return nm
}

// handle applies one move to this node: update held position, re-emit node-geometry.
func (m *nodeMover) handle(msg moveMsg) {
	if msg.NodeID != m.id {
		return
	}
	if msg.Kind == moveMsgKindAnchor {
		// Per-port anchor update: snap to ring-anchor index, mutate this node's held
		// port AnchorId, and re-emit node-geometry so the renderer redraws the port.
		ok := setPortAnchorId(&m.geom, msg.Port, msg.IsInput, msg.AnchorId)
		if !ok {
			return
		}
		if m.tr != nil {
			m.emitGeometry()
		}
		return
	}
	if msg.Kind == moveMsgKindCenter {
		// nodeMover is the SOLE writer of its own position (single-writer by
		// construction — this is the only path that mutates it). A Center payload is
		// the flat absolute-scene-polar drag write from fanCenters: apply it via
		// applyCenter, which also re-emits. A nil Center is fanCenters' PARTNER
		// re-emit (an aimed-port neighbor whose OWN center is unchanged, only asked
		// to re-emit so its port direction picks up the moved partner's fresh center
		// via m.partnerCenter at emit time) — no mutation, just re-emit.
		if msg.Center != nil {
			m.applyCenter(*msg.Center, msg.ReachR)
			return
		}
		// Pure aimed-port re-emit: PartnerCenter (if present) is the SENDER's own
		// just-committed fresh center — update this node's neighborCenters cache for
		// that sender BEFORE re-emitting, so partnerCenter (read at emit time, below)
		// picks up the fresh direction. See moveMsg.PartnerCenter's doc comment for why
		// this must never be read into Center itself.
		if msg.PartnerCenter != nil && msg.SenderID != "" {
			m.neighborCenters[msg.SenderID] = *msg.PartnerCenter
		}
		if m.tr != nil {
			m.emitGeometry()
		}
		return
	}
	if msg.Kind == moveMsgKindDrag {
		// Owner-goroutine drag entry (generalized to EVERY node so no node's quantized
		// offset is ever touched by a foreign mover goroutine): commit this node's OWN
		// new position via the local (synchronous-apply, reported over reportCh) commit path. A drag is
		// always a FREE move now -- there is no equal-radii solve and no self-trigger
		// cascade to run.
		newPos := msg.Target
		if m.commitLocal != nil {
			m.commitLocal(m.id, newPos)
		}
		if m.tr != nil {
			m.tr.Breadcrumb("cascade.root", m.id, "", fmt.Sprintf("newPos=(%.4f,%.4f,%.4f)", newPos.X, newPos.Y, newPos.Z))
		}
		return
	}
	if msg.Kind == moveMsgKindDragStart {
		m.armDragAnchor()
		return
	}
	if msg.Kind == moveMsgKindNeighborSetC {
		// Neighbor edge re-quantize (receiver-computes, one hop, no forward): SenderID
		// (the dragged node) moved to msg.FromCenter; THIS node stays put and re-quantizes
		// its OWN edge to SenderID from the live offset — theta, phi AND r all fresh —
		// so both the angle and the distance to SenderID change (neighborSetCRequantize).
		// Update this node's own neighborCenters cache for SenderID (this message always
		// carries the sender's freshest committed center, regardless of aimed-port
		// status) before dispatching.
		m.neighborCenters[msg.SenderID] = msg.FromCenter
		if m.neighborSetC != nil {
			m.neighborSetC(m.id, msg.SenderID, nodeWorldPos(m.geom), msg.FromCenter, msg.DeltaA, msg.DeltaB, msg.DeltaC)
		}
		return
	}
	if m.tr != nil {
		m.emitGeometry()
	}
}

// armDragAnchor (re-)arms this node's drag-anchor snapshot from its CURRENT
// LocalPolar triples — always overwriting whatever was there, so a new drag on this
// same node re-arms rather than keeping a stale anchor from the previous drag. Runs
// only on this node's own goroutine (moveMsgKindDragStart handler). See
// moveMsgKindDragStart's doc comment for why this fires exactly once per drag.
func (m *nodeMover) armDragAnchor() {
	byTo := map[string]LocalPolar{}
	if m.layoutHolderFn != nil {
		if lh := m.layoutHolderFn(); lh != nil {
			for _, lp := range lh.LocalPolarsSnapshot() {
				byTo[lp.To] = lp
			}
		}
	}
	m.dragAnchorByTo = byTo
	m.dragAnchorArmed = true
}

// applyCenter is the SOLE WRITE of this node's center/reach. It is called ONLY from
// this nodeMover's own inbox-drain goroutine (handle's moveMsgKindCenter case, driven
// by fanCenters below), which is what makes that one goroutine the exclusive writer of
// m.geom. It sets the held polar position, reports the fresh center to the gesture
// goroutine over reportCh (non-blocking, movers→gesture — see posReport's doc
// comment; the gesture goroutine's own drainPositions call is what makes this visible
// to heldCenters/centerOfNode, replacing the old atomic-snapshot cross-goroutine read),
// and re-emits this node's live geometry.
func (m *nodeMover) applyCenter(center vec3, reach float64) {
	setNodeWorld(&m.geom, center)
	m.geom.ReachR = reach
	select {
	case m.reportCh <- posReport{ID: m.id, Center: center}:
	default:
	}
	if m.tr != nil {
		m.emitGeometry()
	}
}

// emitGeometry re-emits this node's authoritative geometry. A CONNECTED port marker is
// AIMED at its partner's current center (m.partnerCenter, atomic-snapshot-backed); an
// edgeless port falls back to its own polar-torus ring-anchor placement (portWorldPos).
// No lock: this method, applyCenter, and setPortAnchorId (via handle) all run on
// nodeMover's own inbox-drain goroutine only (see the doc comment on nodeMover.geom),
// so a plain field read here can never race a concurrent writer.
func (m *nodeMover) emitGeometry() {
	emitNodeGeometryLocked(m.tr, m.id, m.geom, m.partnerCenter)
}

// flushPending retries every message in m.pending in order, attempting a non-blocking
// send to its destination's inbox. A destination whose channel is momentarily full
// stays in the queue (retried next call) — and so does every LATER item addressed to
// that SAME destination, even if its own channel isn't full, so per-destination FIFO
// is preserved (a retained item is never overtaken by a newer one to the same
// destination). An item whose destination doesn't resolve (unknown id) is dropped,
// matching the old deliverMove no-op for an unknown id. Called only from m's own
// goroutine (sendMove, at enqueue time, and run's own loop, every cycle) — no lock
// needed.
func (m *nodeMover) flushPending() {
	if len(m.pending) == 0 || m.resolveDest == nil {
		return
	}
	blocked := map[string]bool{}
	kept := m.pending[:0]
	for _, item := range m.pending {
		if blocked[item.destID] {
			kept = append(kept, item)
			continue
		}
		ch, ok := m.resolveDest(item.destID)
		if !ok {
			continue
		}
		select {
		case ch <- item.msg:
		default:
			blocked[item.destID] = true
			kept = append(kept, item)
		}
	}
	m.pending = kept
}

// run is the node's per-goroutine move loop. It paces itself on its OWN clock copy the
// same way every other loop in the system does (edgeMover.run, DriveHeld,
// emitRefillSlide): a Clock.Copy()
// taken once here at goroutine start, ApplySpeedNonBlocking polled once per cycle, and
// SleepCycle(ctx) as the pacing sleep at the bottom of the loop. It used to be the odd
// loop out, blocking on a reflect.Select over its whole channel set instead; that is
// gone.
//
// Each cycle FIRST drains every one of its OWN dedicated inbound channels (extIn + one
// per neighbor, see the type's doc comment) — there is no shared inbox to drain
// — non-blockingly and acts on
// whatever is there, repeating the drain pass until a full pass finds nothing left (so a
// backlog on any one channel is fully drained before the cycle paces, not throttled to
// "one message per channel per cycle"), THEN retries any pending sends, THEN sleeps one
// clock cycle. Nothing here ever blocks on a receive OR a send: an empty channel just
// falls through its `default`, exactly the "read non-blockingly at the top, act on what's
// there, pace on the clock" shape the design calls for. This does not busy-wait (the
// pacing sleep bounds every cycle to one clock tick regardless of whether the drain pass
// found anything), and does not throttle a real backlog (a full pass draining every
// channel to empty runs entirely within the cycle, before the sleep — a burst of incoming
// messages is drained as fast as it arrives, not capped at one per channel per tick).
func (m *nodeMover) run(ctx context.Context) {
	if m.clockSrc != nil {
		m.clk = m.clockSrc.Copy()
	}
	for {
		ApplySpeedNonBlocking(m.clk, m.speedCh)
		// Drain every dedicated inbound channel non-blockingly, repeating until a
		// full pass yields nothing — this is the "drain to empty, don't throttle a
		// backlog" half of the shape.
		for {
			progressed := false
			select {
			case <-ctx.Done():
				return
			case msg := <-m.extIn:
				m.handle(msg)
				if msg.testDone != nil {
					close(msg.testDone)
				}
				progressed = true
			default:
			}
			for _, ch := range m.neighborIn {
				select {
				case msg := <-ch:
					m.handle(msg)
					if msg.testDone != nil {
						close(msg.testDone)
					}
					progressed = true
				default:
				}
			}
			if !progressed {
				break
			}
		}
		// Retry any pending sends (nm.pending/flushPending) every cycle — a
		// destination that was full earlier may have drained since.
		m.flushPending()
		if err := m.clk.SleepCycle(ctx); err != nil {
			return
		}
	}
}

// edgeMover owns one edge. It holds both endpoint geometries and recomputes its own
// segment/arc on an endpoint move (the edge label, which keys its channels below,
// encodes the two connected nodes).
type edgeMover struct {
	edgeID  string
	srcID   string
	dstID   string
	srcH    string
	dstH    string
	srcGeom nodeGeom
	dstGeom nodeGeom
	out     *Out       // source Out for this edge (per-edge segment/arc/latency)
	dest    *PacedWire // dest wire (in-flight revision + latency aggregate)
	// extIn is this edge's dedicated channel for EXTERNAL entries (gesture.go's
	// applyRingAnchor anchor mail-sort). srcIn/dstIn are this edge's two dedicated
	// channels FROM its two endpoint nodes' own goroutines — srcIn written only by
	// srcID's nodeMover, dstIn only by dstID's — the literal "two channels" the design
	// specifies, one per direction this edge can be told about a moved endpoint.
	// Nothing else ever writes on any of the three.
	extIn chan moveMsg
	srcIn chan moveMsg
	dstIn chan moveMsg
	tr    *T.Trace
	// clockSrc is the Clock this edgeMover's own goroutine (run) Copies from
	// EXACTLY ONCE at its own start, into clk below. Not read again afterward.
	clockSrc Clock
	// clk is this edgeMover's OWN clock copy, set once by run() at goroutine
	// start. Only this goroutine (handle, called from run's loop) ever reads
	// it, so no lock is needed. Defaults to a fresh, real, live-ticking
	// RealClock (see newEdgeMover) so a test that calls handle() directly
	// without launching run() as a goroutine never dereferences a nil Clock —
	// per-goroutine-clock.md's API demolition deleted the old inert/zero-Tick
	// placeholder (item 3), so the only non-nil default left is a genuine
	// clock, not a fake stand-in.
	clk Clock
	// speedCh delivers a speed change to THIS edgeMover's own clk copy
	// (per-goroutine-clock.md "Delivery"). Set once, at construction
	// (newMoveDispatch), from the loader's build-wide speed-sink accumulator;
	// nil in bare test construction, which is fine — a nil channel is never
	// selected in run()'s loop below.
	speedCh chan float64
}

func newEdgeMover(ep EdgeEndpoints, edgeID string, srcGeom, dstGeom nodeGeom, tr *T.Trace, clockSrc Clock) *edgeMover {
	// clk defaults to a fresh RealClock (its own independent origin — fine here:
	// this default is only ever read by a test calling handle() directly, never by
	// production, where run() always overwrites it below with clockSrc.Copy() before
	// the goroutine does anything else) so a test that calls handle() directly
	// (without launching run() as a goroutine) never dereferences a nil Clock;
	// run() overwrites it with a real per-goroutine copy at start.
	return &edgeMover{
		edgeID:   edgeID,
		srcID:    ep.Source,
		dstID:    ep.Target,
		srcH:     ep.SourceHandle,
		dstH:     ep.TargetHandle,
		srcGeom:  srcGeom,
		dstGeom:  dstGeom,
		extIn:    make(chan moveMsg, 8),
		srcIn:    make(chan moveMsg, 8),
		dstIn:    make(chan moveMsg, 8),
		tr:       tr,
		clockSrc: clockSrc,
		clk:      NewRealClock(),
	}
}

// handle applies one inbox message to this edge. For a move message it updates the
// matching endpoint geom, recomputes the edge's segment + arc, writes them onto the
// source Out, revises any in-flight bead, emits the new edge geometry, and updates
// the dest port's latency aggregate. A move that touches neither endpoint is ignored.
func (m *edgeMover) handle(msg moveMsg) {
	if msg.Kind == moveMsgKindAnchor {
		// A port-anchor change recomputes this edge's segment/arc only if the changed
		// port is one of THIS edge's endpoints (matching node id, port name, direction).
		// Source endpoint is an OUTPUT (isInput==false); target endpoint is an INPUT.
		switch {
		case msg.NodeID == m.srcID && !msg.IsInput && msg.Port == m.srcH:
			if !setPortAnchorId(&m.srcGeom, msg.Port, false, msg.AnchorId) {
				return
			}
		case msg.NodeID == m.dstID && msg.IsInput && msg.Port == m.dstH:
			if !setPortAnchorId(&m.dstGeom, msg.Port, true, msg.AnchorId) {
				return
			}
		default:
			return
		}
		m.recomputeGeometry()
		return
	}
	if msg.Kind == moveMsgKindCenter {
		// Polar re-propagation: adopt the centrally-computed center on whichever
		// endpoint this message names, then recompute the edge.
		if msg.Center == nil {
			return
		}
		switch msg.NodeID {
		case m.srcID:
			setNodeWorld(&m.srcGeom, *msg.Center)
		case m.dstID:
			setNodeWorld(&m.dstGeom, *msg.Center)
		default:
			return
		}
		m.recomputeGeometry()
		return
	}
	if msg.Kind == moveMsgKindCenters {
		// Batched polar re-propagation: apply every moved endpoint this edge owns,
		// then recompute ONCE. An edge whose both endpoints moved in one frame would
		// otherwise recompute (and emit) twice — the duplicate-emit source on a node-2
		// drag, where the dragged node and its sphere center both move.
		moved := false
		if c, ok := msg.Centers[m.srcID]; ok {
			setNodeWorld(&m.srcGeom, c)
			moved = true
		}
		if c, ok := msg.Centers[m.dstID]; ok {
			setNodeWorld(&m.dstGeom, c)
			moved = true
		}
		if moved {
			m.recomputeGeometry()
		}
		return
	}
	// Plain "move" messages have no effect under the polar layout;
	// position updates arrive as "center" messages instead.
	_ = msg
}

// recomputeGeometry re-derives this edge's segment/arc/latency from its held endpoint
// geoms+handles and propagates them: write onto the source Out, revise any in-flight
// bead (fraction-preserving), update the dest port window aggregate, and emit the new
// segment so the renderer redraws the wire. Shared by node-move and port-anchor handling.
func (m *edgeMover) recomputeGeometry() {
	seg := edgeSegment(m.srcGeom, m.dstGeom, m.srcH, m.dstH)
	arc := edgeArcPolar(m.srcGeom, m.dstGeom, m.srcH, m.dstH)
	lat := arc / PulseSpeedWuPerMs

	// Publish the new per-edge segment/arc/latency onto the source Out as an immutable
	// snapshot so the next placement (on the source node goroutine) reads the new
	// segment via an atomic load — no data race with recomputeGeometry's write here.
	if m.out != nil {
		m.out.publishGeom(outGeom{ArcLength: arc, SimLatencyMs: lat, Start: seg.Start, End: seg.End})
	}
	// Re-derive an in-flight bead on this edge from the new arc + segment (no-op if
	// none in flight); this runs on the SAME goroutine that owns the dest wire's
	// bead state (this is that wire's own goroutine — see edgeMover.run), so no
	// lock is needed.
	if m.dest != nil {
		m.dest.ReviseInFlightGeometry(m.clk.Tick(), arc, seg)
	}
	// Emit this edge's own segment so the renderer redraws the wire from Go's endpoints.
	if m.tr != nil {
		m.tr.Geometry(m.edgeID, m.srcID, m.dstID, m.srcH, m.dstH,
			seg.Start.X, seg.Start.Y, seg.Start.Z,
			seg.End.X, seg.End.Y, seg.End.Z)
	}
}

// run is the edge's per-goroutine loop. It IS the wire's own goroutine
// (MODEL.md "The network" — PacedWire is an active goroutine, and it is this
// same per-edge goroutine that already existed to revise in-flight geometry,
// not an additional one): every cycle it drains any pending move/speed
// messages without blocking, then drives its dest wire's ONE cycle of bead
// ownership (DriveOneCycle — placement drain, position-step, delivery
// handoff), then paces to the next cycle on its OWN clock copy. This is what
// lets ReviseInFlightGeometry (called from handle, below, on this SAME
// goroutine) touch pw.inflight with no lock: there is exactly one goroutine
// on either side of that call.
func (m *edgeMover) run(ctx context.Context) {
	// Copy taken ONCE at this goroutine's start (run IS the goroutine). If no clockSrc was
	// given (bare test construction), keep the inert placeholder newEdgeMover
	// seeded m.clk with.
	if m.clockSrc != nil {
		m.clk = m.clockSrc.Copy()
	}
	for {
		// Drain extIn/srcIn/dstIn/speedCh without blocking, so a cycle always reaches
		// the wire-drive step below even with nothing queued. Three dedicated channels,
		// not one shared inbox: extIn (external gesture entries), srcIn (this edge's
		// source node's own goroutine), dstIn (this edge's target node's own goroutine).
	drain:
		for {
			select {
			case <-ctx.Done():
				return
			case sp := <-m.speedCh:
				// Delivery (per-goroutine-clock.md): apply directly to this
				// goroutine's own clk copy — nothing else reaches it.
				if rc, ok := m.clk.(*RealClock); ok {
					rc.SetSpeed(sp)
				}
			case msg := <-m.extIn:
				m.handle(msg)
				if msg.testDone != nil {
					close(msg.testDone)
				}
			case msg := <-m.srcIn:
				m.handle(msg)
				if msg.testDone != nil {
					close(msg.testDone)
				}
			case msg := <-m.dstIn:
				m.handle(msg)
				if msg.testDone != nil {
					close(msg.testDone)
				}
			default:
				break drain
			}
		}
		if m.dest != nil {
			m.dest.DriveOneCycle(ctx, m.clk.Tick())
		}
		if err := m.clk.SleepCycle(ctx); err != nil {
			return
		}
	}
}
