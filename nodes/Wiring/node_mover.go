// node_mover.go — the per-node/per-edge mover actor types split out of node_move.go. Pure
// move: no logic changes beyond the two-channels-no-inbox-no-blocking restructure
// (docs/planning/visual-editor/outbox-two-channels.md). node_move.go retains the dispatch
// registry (MoveDispatch) that routes messages to these actors; this file owns the actors
// themselves — nodeMover (owns one node's geometry, its own inbox, and its own outbound
// retry queue) and edgeMover (owns one edge's segment/arc + in-flight bead revision). Each
// mover touches MoveDispatch only via an injected enqueue func — no back-reference to the
// dispatch registry, and no shared queue/lock between movers.

package Wiring

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	T "github.com/dtauraso/wirefold/Trace"
)

// pendingSend is one (destination, message) pair this node's own goroutine tried to
// deliver, failed (the target's inbox was momentarily full), and is retrying — see
// nodeMover.pending's doc comment. There is no separate sender goroutine and no lock:
// only nm's own goroutine ever reads or writes nm.pending (docs/planning/visual-editor/
// outbox-two-channels.md).
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

// nodeMover owns one node's geometry. It reads its inbox in its own goroutine and,
// on a move for itself, updates its held position and re-emits its node-geometry.
type nodeMover struct {
	id    string
	geom  nodeGeom
	inbox chan moveMsg
	tr    *T.Trace
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
	// snap is an atomically-published immutable snapshot of this node's current
	// center+reachR. Written only by the mover's own goroutine after every center
	// update; read by any goroutine (stdin reader) to observe the current position
	// without crossing into the mover's live geom.
	snap atomic.Pointer[centerSnap]
	// sendMove routes a moveMsg to another id's inbox by id-lookup against md.dispatch (a
	// read-only directory after construction — no queue, no shared mutable state).
	// Bound to md.enqueueFor(nm): it appends to nm.pending and immediately attempts a
	// non-blocking flush (never blocks the calling handler goroutine).
	sendMove func(id string, msg moveMsg)
	edgeIDs  []string
	// centerOf resolves another node's current world center, bound to
	// md.centerOfNode. Unused by any live handler now that the rule/gate/anchor
	// cascade (which used it to read rule-neighbor centers) is gone; kept wired for
	// any future direct-neighbor lookup need.
	centerOf func(id string) (vec3, bool)
	// commitLocal is the OWNER-GOROUTINE commit path, bound to
	// md.commitNodeMoveLocal (generalized to every node). It publishes this node's
	// own snap SYNCHRONOUSLY via applyCenter instead of enqueuing an async self-send,
	// so it is safe to call from THIS node's own handle() for a moveMsgKindDrag, with
	// no cross-goroutine self-send and no shared mutable state (each node's quantized
	// offset lives on its own mover — see nodeMover.quantOffset). nil in tests that
	// build a bare nodeMover directly.
	commitLocal func(id string, newPos vec3)
	// partnerCenter resolves, per (port,isInput) on this node, the CURRENT world center of
	// the single partner node connected via one edge (aimed-port model, port_geometry.go
	// portWorldPosAimed / builders.go partnerCenterFn). Wired by newMoveDispatch from
	// b.edgeEndpoints + the OTHER nodeMover's atomic snap — a dynamic, always-current lookup
	// with no shared mutable state. nil only in tests that build a bare nodeMover directly.
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
	// md.neighborSetCReposition. Dispatched from moveMsgKindNeighborSetC so a domain
	// neighbor's holder AND world position are written only by that neighbor's OWN
	// goroutine. nil in tests that build a bare nodeMover directly.
	neighborSetC func(selfID, fromID string, fromCenter vec3, deltaA, deltaB, deltaC int)
	// pending is THIS node's own outbound retry queue (docs/planning/visual-editor/
	// outbox-two-channels.md): sendMove appends here and attempts an immediate
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
	// resolveDest looks up a destination id's inbox channel in md.dispatch (a
	// read-only directory after construction, safe to read from any goroutine).
	// nil only in tests that build a bare nodeMover directly, in which case
	// flushPending is a no-op.
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

func newNodeMover(id string, geom nodeGeom, tr *T.Trace) *nodeMover {
	nm := &nodeMover{id: id, geom: geom, inbox: make(chan moveMsg, 8), tr: tr}
	// Seed the atomic snapshot from the initial geometry (even when !HasPos, in which case
	// nodeWorldPos falls back to the origin) so readers — including another node's aimed-port
	// partnerCenter lookup — always have a valid center to read before the first center
	// message arrives.
	nm.snap.Store(&centerSnap{c: nodeWorldPos(geom), p: geom.ScenePolar, reach: geom.ReachR})
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
		if m.tr != nil {
			m.emitGeometry()
		}
		return
	}
	if msg.Kind == moveMsgKindDrag {
		// Owner-goroutine drag entry (generalized to EVERY node so no node's quantized
		// offset is ever touched by a foreign mover goroutine): commit this node's OWN
		// new position via the local (synchronous-snap-publish) commit path. A drag is
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
		if m.neighborSetC != nil {
			m.neighborSetC(m.id, msg.SenderID, msg.FromCenter, msg.DeltaA, msg.DeltaB, msg.DeltaC)
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
// m.geom/m.snap. It sets the held polar position, publishes the atomic snapshot readers
// observe cross-goroutine (stdin reader: centerOfNode/heldCenters/heldPolar/fanCenters'
// partner lookup, edgeMover's partnerCenter), and re-emits this node's live geometry.
func (m *nodeMover) applyCenter(center vec3, reach float64) {
	setNodeWorld(&m.geom, center)
	m.geom.ReachR = reach
	m.snap.Store(&centerSnap{c: center, p: m.geom.ScenePolar, reach: reach})
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

// run is the node's per-goroutine move loop. A message on the inbox wakes it
// IMMEDIATELY (a blocking receive, exactly like before this restructure) — an early
// version of this loop instead read the inbox non-blockingly and unconditionally slept
// one whole clock cycle before checking again, which throttled this node's own bounded
// inbox to "buffer-size delivered per cycle" under sustained concurrent load (measured:
// TestMutuallyAdjacentDragFloodNoDeadlock, which passes in ~0.1s under -race on the
// blocking-receive shape, TIMED OUT at 10s on the poll-then-always-sleep shape — a real
// throughput regression, not a flake). The ONLY thing that must never block is a SEND
// (nm.pending/flushPending, below); receiving was never the deadlock's cause and gains
// nothing from being non-blocking. When the inbox is genuinely idle, this falls back to
// pacing one wall tickPeriod (time.After) before checking again — no separate Clock
// copy is needed here (unlike edgeMover, nodeMover drives no sim-time-scaled bead
// motion, only retries a send), so it never spins hot polling nothing
// (docs/planning/visual-editor/outbox-two-channels.md "watch for"); pending sends are
// retried after every handled message AND on every idle cycle tick.
func (m *nodeMover) run(ctx context.Context) {
	for {
		// A plain blocking select (no `default`): this parks the goroutine — no
		// polling, no spin — until EITHER a message arrives (woken immediately, same
		// latency as a bare blocking receive) OR one clock cycle elapses with nothing
		// arriving (the idle-retry tick, so a pending send that failed earlier still
		// gets retried on a steady cadence even during a lull). ctx.Done() unparks it
		// on shutdown.
		select {
		case <-ctx.Done():
			return
		case msg := <-m.inbox:
			m.handle(msg)
			if msg.testDone != nil {
				close(msg.testDone)
			}
			// A message just arrived and was handled (which may itself have enqueued
			// new outbound sends) — retry pending sends now, then loop straight back
			// to select again with NO artificial delay, so a burst of incoming
			// messages is drained as fast as they arrive (this select blocks again
			// immediately if another message is already queued, same as a bare
			// blocking receive would).
			m.flushPending()
		case <-time.After(tickPeriod):
			// Idle cycle: nothing arrived. Retry any pending sends (a destination
			// that was full earlier may have drained since) before looping back to
			// wait again.
			m.flushPending()
		}
	}
}

// edgeMover owns one edge. It holds both endpoint geometries and recomputes its own
// segment/arc on an endpoint move (the edge label, which keys its inbox, encodes the
// two connected nodes).
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
	inbox   chan moveMsg
	tr      *T.Trace
	// clockSrc is the Clock this edgeMover's own goroutine (run) Copies from
	// EXACTLY ONCE at its own start (docs/planning/visual-editor/
	// per-goroutine-clock.md), into clk below. Not read again afterward.
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
		inbox:    make(chan moveMsg, 8),
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
		m.tr.Geometry(m.edgeID, m.srcID, m.dstID,
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
	// Copy taken ONCE at this goroutine's start (run IS the goroutine) —
	// docs/planning/visual-editor/per-goroutine-clock.md. If no clockSrc was
	// given (bare test construction), keep the inert placeholder newEdgeMover
	// seeded m.clk with.
	if m.clockSrc != nil {
		m.clk = m.clockSrc.Copy()
	}
	for {
		// Drain any pending move/speed messages without blocking, so a cycle
		// always reaches the wire-drive step below even with nothing queued.
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
			case msg := <-m.inbox:
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
