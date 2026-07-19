// node_move.go — decentralized node-move handling.
//
// A node-move is NOT handled by a central coordinator. Instead the loader builds
// a pure dispatch registry (key → inbox channel) whose keys are BOTH node ids AND
// edge ids (the edge label, which encodes the two connected nodes). The stdin
// reader's whole job for a move is a mail-sort: for each (key,value) entry in the
// message, push value onto channels[key]. No recompute, no topology logic lives in
// the reader.
//
// Two kinds of mover own the work, each in its own goroutine (MODEL.md: each node
// and each wire is a goroutine; geometry emission is per-goroutine —
// memory/feedback_per_goroutine_bridge.md):
//
//   - nodeMover: owns ONE node's geometry. On a move for itself it updates its own
//     held position and re-emits its own node-geometry (emitNodeGeometry).
//   - edgeMover: owns ONE edge. It holds BOTH endpoint nodeGeoms (set at load). On
//     a move of either endpoint it updates the matching endpoint, recomputes its
//     OWN segment + arc (segmentBetweenPorts/arcLengthBetweenPorts), writes them
//     onto the source Out (next placement reads them), revises any in-flight bead's
//     remaining travel on the dest wire (ReviseInFlightGeometry, fraction-preserving),
//     and emits its OWN edge geometry (tr.Geometry).
//
// This reproduces, per-goroutine, exactly what the old central applyNodeMove did in
// one stdin goroutine: same node-geometry emit, same per-edge segment/arc recompute,
// same in-flight revision, same edge-geometry emit.

package Wiring

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"sync/atomic"

	T "github.com/dtauraso/wirefold/Trace"
)

// moveMsgKind discriminates moveMsg payloads.
const (
	// The node-move kind ("move", the zero value "") carries no payload and is a
	// no-op in every mover switch, so it has no constant — the switches simply
	// fall through. The remaining kinds each select a distinct payload.
	moveMsgKindAnchor  = "anchor"  // per-port anchor update (drag along the ring)
	moveMsgKindCenter  = "center"  // polar-layout re-propagated world center for one node
	moveMsgKindCenters = "centers" // batched centers for an edge: update both endpoints, recompute ONCE
	// moveMsgKindDrag is a node's own-goroutine drag entry: the drag itself is routed
	// to the dragged node's OWN inbox instead of the stdin reader committing on its
	// behalf. The receiver commits its OWN new position via the owner-goroutine commit
	// path (commitNodeMoveLocal, which publishes its snap SYNCHRONOUSLY via
	// applyCenter). A drag is always a FREE move -- no equal-radii solve, no
	// self-trigger cascade.
	moveMsgKindDrag = "drag"
	// moveMsgKindNeighborSetC: the plain-neighbor / general edge-length propagation
	// (requantizeLocalPolars' per-neighbor fan). A dragged node X sends EVERY direct
	// domain neighbor M this SINGLE ASSIGNMENT -- the new quantized edge length SnapC
	// (X's own freshly-requantized c to M) and X's fresh FromCenter. M does NOT
	// re-derive its stored bearing to X: it KEEPS its own persisted
	// QuantITheta/QuantIPhi to SenderID exactly as stored, writes ONLY the new c onto
	// that record, and repositions itself at
	// FromCenter - dir(storedTheta,storedPhi about M's own pole)*newR -- sliding along
	// its existing viewing direction to the new distance, X held fixed. One hop only: M
	// never forwards this to its own neighbors, and this never runs any further cascade
	// (see neighborSetCReposition).
	moveMsgKindNeighborSetC = "neighborSetC"
)

// centerSnap is an immutable snapshot of a node's position published by the nodeMover
// via an atomic.Pointer so readers on other goroutines (stdin reader, etc.) can observe
// the current center without touching the mover's live geom.
type centerSnap struct {
	c     vec3  // world center — cartesian, published for the emit/input boundaries only
	p     polar // scene polar (r,θ,φ about the scene center) — the polar source of truth for geometry math
	reach float64
}

// moveMsg is one entry routed to a mover's inbox. kind selects the payload:
//   - "" or "move": node-move — currently a no-op (polar-layout positions all nodes via "center" messages).
//
// Every PRODUCTION send is fire-and-forget: the sender drops the message in an inbox
// and returns. No production path observes the receiver finishing — a node does its own
// local work on its own goroutine and drives its own outputs (MODEL.md: no ack, no
// send-gating, no delivery signal).
//
// testDone is the one exception and it is NOT a production mechanism: it exists only so
// a test can block until an async mover has handled a message before asserting (see
// node_move_test.go's `deliver`). It is needed because an edgeMover publishes no atomic
// snapshot a test could safely poll. Production ALWAYS leaves it nil — if you find
// yourself setting it outside a _test.go file, you are reintroducing the ack the model
// forbids; make the receiver's own goroutine do the work instead.
type moveMsg struct {
	Kind   string
	NodeID string
	// Anchor payload (Kind == "anchor"): identify the port whose anchor changed.
	// Port/IsInput name the port on NodeID; AnchorId is the snapped ring-anchor index
	// (Go snaps from the incoming world-space direction; TS never computes the index).
	Port     string
	IsInput  bool
	AnchorId int
	// Center (Kind == "center"): the re-propagated world center for NodeID under the
	// polar layout. Each owning node/edge goroutine writes it onto its held geom
	// and re-emits its own geometry. (RootMove is now a thin wrapper over
	// rootMoveViaMessages — the decentralized node-to-node message cascade; there is
	// no central fan-out step.)
	Center *vec3
	// Centers (Kind == "centers"): batched per-edge re-propagation. Maps node id → new
	// world center for every moved endpoint of THIS edge in a single frame, so an edge
	// whose BOTH endpoints moved updates both and recomputes/emits its segment ONCE
	// instead of once per endpoint message (the node-2 drag duplicate-emit fix).
	Centers map[string]vec3
	// ReachR (Kind == "center"): the re-propagated sphere REACH radius for NodeID (max
	// distance to a surface child under the new centers). The nodeMover writes it onto its
	// held geom so the re-emitted node-geometry streams the correct sphereR during a drag.
	ReachR float64
	// FromCenter (Kind == "neighborSetC"): the SENDER's (SenderID's) fresh committed
	// world center. The receiver repositions itself along its OWN stored bearing to
	// SenderID at the new distance (see neighborSetCReposition) — receiver-computes.
	FromCenter vec3
	// SenderID (Kind == "neighborSetC"): the id of the mover whose fresh FromCenter
	// the receiver repositions itself relative to.
	SenderID string
	// SnapC (Kind == "neighborSetC"): the new quantized edge length (whole ticks of
	// the receiver's own step constant) to write onto the receiver's own LocalPolar
	// record to SenderID.
	SnapC int
	// Target (Kind == "drag"): the raw drag target world position for NodeID's
	// owner-goroutine commit. Every node is a free move, so this is committed as-is.
	Target vec3
	// testDone: see the type comment. Test-only; production leaves it nil.
	testDone chan struct{}
}

// outboxItem is one queued (destination, message) pair awaiting delivery by a mover's
// dedicated sender goroutine.
type outboxItem struct {
	destID string
	msg    moveMsg
}

// outbox is a per-mover UNBOUNDED FIFO of outgoing messages, decoupling *enqueue* (done
// by the mover's own inbox-drain goroutine, inside handle — must never
// block) from *delivery* (done by a dedicated sender goroutine, which blocks on the
// target's inbox exactly like the old synchronous send did). See
// docs/planning/visual-editor/cascade-deadlock-fix.md: two mutually-adjacent movers,
// both mid-handle with full inboxes, would otherwise deadlock each blocking a send into
// the other's full inbox while neither drains. Unbounded by design — a bounded queue
// reintroduces the same blocking-enqueue deadlock; cascades are finite (idempotent
// quiescence) so the queue drains in practice. Nothing is ever dropped and per-target
// order is exactly enqueue order (a single sender pops FIFO), so "latest wins" still
// holds — this defers delivery by a goroutine hop, it never re-derives or reorders.
type outbox struct {
	mu     sync.Mutex
	cond   *sync.Cond
	q      []outboxItem
	closed bool
}

func newOutbox() *outbox {
	ob := &outbox{}
	ob.cond = sync.NewCond(&ob.mu)
	return ob
}

// enqueue appends one item to the tail of the queue. Never blocks (unbounded slice
// append) — this is what keeps the calling handler goroutine free to return to draining
// its own inbox.
func (ob *outbox) enqueue(destID string, msg moveMsg) {
	ob.mu.Lock()
	ob.q = append(ob.q, outboxItem{destID: destID, msg: msg})
	ob.cond.Signal()
	ob.mu.Unlock()
}

// run is the mover's dedicated sender goroutine: pop items in FIFO order and deliver
// each via send (the existing BLOCKING channel write into the target's inbox). Exits
// promptly on ctx cancellation (process shutdown — no need to flush; draining a queue on
// a dying process delivers into inboxes nobody will ever read again).
func (ob *outbox) run(ctx context.Context, send func(destID string, msg moveMsg)) {
	cancelWatcherDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			ob.mu.Lock()
			ob.closed = true
			ob.cond.Broadcast()
			ob.mu.Unlock()
		case <-cancelWatcherDone:
		}
	}()
	defer close(cancelWatcherDone)
	for {
		ob.mu.Lock()
		for len(ob.q) == 0 && !ob.closed {
			ob.cond.Wait()
		}
		if len(ob.q) == 0 && ob.closed {
			ob.mu.Unlock()
			return
		}
		item := ob.q[0]
		ob.q = ob.q[1:]
		ob.mu.Unlock()
		send(item.destID, item.msg)
	}
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
	// geomMu guards m.geom's full-struct read in emitGeometry (which runs on a
	// different goroutine) against the field writes, which ALL happen on nodeMover's
	// own inbox-drain goroutine: applyCenter (the sole writer of position fields) and
	// handle's anchor/default cases (the sole writer of port-anchor fields). There is
	// no separate per-node "Update()" writer goroutine — that was the retired SLICE 3
	// architecture; position is single-writer by construction (only applyCenter, on
	// the mover goroutine, ever writes it). This mutex exists purely so emitGeometry's
	// cross-goroutine read never races a concurrent field write — it is NOT a second
	// position-writer.
	geomMu sync.Mutex
	// snap is an atomically-published immutable snapshot of this node's current
	// center+reachR. Written only by the mover's own goroutine after every center
	// update; read by any goroutine (stdin reader) to observe the current position
	// without crossing into the mover's live geom.
	snap atomic.Pointer[centerSnap]
	// sendMove routes a moveMsg to another id's inbox by id-lookup against md.dispatch (a
	// read-only directory after construction — no queue, no shared mutable state).
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
	neighborSetC func(selfID, fromID string, fromCenter vec3)
	// outbox is this node's own outbound-message queue (see the outbox type doc
	// comment). sendMove enqueues onto it (never blocks); a dedicated sender goroutine
	// (spawned in MoveDispatch.Start) pops it in FIFO order and does the actual
	// blocking delivery. This is what lets handle's sends stay non-blocking while
	// every OTHER send-in-handle property (order, no drops) is unchanged.
	outbox *outbox
}

func newNodeMover(id string, geom nodeGeom, tr *T.Trace) *nodeMover {
	nm := &nodeMover{id: id, geom: geom, inbox: make(chan moveMsg, 8), tr: tr, outbox: newOutbox()}
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
		m.geomMu.Lock()
		ok := setPortAnchorId(&m.geom, msg.Port, msg.IsInput, msg.AnchorId)
		m.geomMu.Unlock()
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
	if msg.Kind == moveMsgKindNeighborSetC {
		// Neighbor edge re-quantize (receiver-computes, one hop, no forward): SenderID
		// (the dragged node) moved to msg.FromCenter; THIS node stays put and re-quantizes
		// its OWN edge to SenderID from the live offset — theta, phi AND r all fresh —
		// so both the angle and the distance to SenderID change (neighborSetCRequantize).
		if m.neighborSetC != nil {
			m.neighborSetC(m.id, msg.SenderID, msg.FromCenter)
		}
		return
	}
	if m.tr != nil {
		m.emitGeometry()
	}
}

// applyCenter is the SOLE WRITE of this node's center/reach. It is called ONLY from
// this nodeMover's own inbox-drain goroutine (handle's moveMsgKindCenter case, driven
// by fanCenters below), which is what makes that one goroutine the exclusive writer of
// m.geom/m.snap. It sets the held polar position, publishes the atomic snapshot readers
// observe cross-goroutine (stdin reader: centerOfNode/heldCenters/heldPolar/fanCenters'
// partner lookup, edgeMover's partnerCenter), and re-emits this node's live geometry.
func (m *nodeMover) applyCenter(center vec3, reach float64) {
	m.geomMu.Lock()
	setNodeWorld(&m.geom, center)
	m.geom.ReachR = reach
	scenePolar := m.geom.ScenePolar
	m.geomMu.Unlock()
	m.snap.Store(&centerSnap{c: center, p: scenePolar, reach: reach})
	if m.tr != nil {
		m.emitGeometry()
	}
}

// emitGeometry re-emits this node's authoritative geometry. A CONNECTED port marker is
// AIMED at its partner's current center (m.partnerCenter, atomic-snapshot-backed); an
// edgeless port falls back to its own polar-torus ring-anchor placement (portWorldPos).
// geomMu-guarded: takes a local copy of m.geom under the lock so the actual emit (which
// can be slow — trace serialization) does not hold the lock against the other writer.
func (m *nodeMover) emitGeometry() {
	m.geomMu.Lock()
	geom := m.geom
	m.geomMu.Unlock()
	emitNodeGeometryLocked(m.tr, m.id, geom, m.partnerCenter)
}

// run is the node's per-goroutine move loop: drain the inbox until ctx is done.
func (m *nodeMover) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-m.inbox:
			m.handle(msg)
			if msg.testDone != nil {
				close(msg.testDone)
			}
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
}

func newEdgeMover(ep EdgeEndpoints, edgeID string, srcGeom, dstGeom nodeGeom, tr *T.Trace) *edgeMover {
	return &edgeMover{
		edgeID:  edgeID,
		srcID:   ep.Source,
		dstID:   ep.Target,
		srcH:    ep.SourceHandle,
		dstH:    ep.TargetHandle,
		srcGeom: srcGeom,
		dstGeom: dstGeom,
		inbox:   make(chan moveMsg, 8),
		tr:      tr,
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
	// none in flight); the dest wire owns the bead under its own mutex.
	if m.dest != nil {
		m.dest.ReviseInFlightGeometry(arc, seg)
	}
	// Emit this edge's own segment so the renderer redraws the wire from Go's endpoints.
	if m.tr != nil {
		m.tr.Geometry(m.edgeID, m.srcID, m.dstID,
			seg.Start.X, seg.Start.Y, seg.Start.Z,
			seg.End.X, seg.End.Y, seg.End.Z)
	}
}

// run is the edge's per-goroutine move loop: drain the inbox until ctx is done.
func (m *edgeMover) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-m.inbox:
			m.handle(msg)
			if msg.testDone != nil {
				close(msg.testDone)
			}
		}
	}
}

// MoveDispatch is the pure key→inbox dispatch registry built at load. Keys are BOTH
// node ids AND edge ids; the stdin reader mail-sorts each move entry to channels[key].
// It also retains the per-edge source Outs so out-of-package test/verifier callers can
// read an edge's loaded geometry (EdgeOut) without going through a central coordinator.
type MoveDispatch struct {
	dispatch   map[string]chan moveMsg
	nodeMovers map[string]*nodeMover
	edgeMovers map[string]*edgeMover
	// edgeOut: edgeId → source *Out, for read-only access by tests/verifiers.
	edgeOut map[string]*Out
	// nodeSeeds/edgeSeeds are every node/edge's load-time seed geometry, captured ONCE at
	// construction (newMoveDispatch) in spec order — the deterministic directory-sorted
	// order LoadTopology read the topology in, NOT map iteration order. Exposed via
	// NodeSeeds/EdgeSeeds so main.go can seed the buffer's row tables from the diagram
	// itself before any node goroutine starts (CLAUDE.md: rows are a projection of the
	// diagram, not a discovery log built by racing goroutines to their first emit).
	nodeSeeds []NodeGeomSeed
	edgeSeeds []EdgeGeomSeed
	// sceneSphere is the first-class scene reference every node's SCENE polar is measured
	// about (polar-model.md, sphere_layout.go). Loaded from scene.json (or defaulted from
	// the content-fit) at startup; its Center is the one cartesian anchor. Phase 1 stores
	// it; later phases derive node world from it and move it on pan.
	sceneSphere sceneSphere
	// vp is the polar camera viewpoint state (viewpoint_state.go). Owned entirely by
	// MoveDispatch — no separate goroutine; callers serialize externally (stdin reader
	// runs in a single goroutine). MoveDispatch exposes thin delegating methods.
	vp viewpointState
	// tr is the trace sink (retained for trace emission; diagnostic breadcrumbs removed).
	tr *T.Trace
	// ov groups the 9 overlay-toggle visibility booleans and their flip/emit logic
	// (overlay_state.go). Initialized to defaults by newMoveDispatch (all true).
	// MoveDispatch exposes thin delegating methods.
	ov overlayState
	// gest is the gesture state machine (gesture.go): it consumes raw pointer/wheel input
	// and produces camera (viewpoint) + topology (node-move) changes. Owned by
	// MoveDispatch; serialized by the single-goroutine stdin reader. Zero value = idle.
	gest gestureState
	// vpPersist is the debounced camera-viewpoint persister (scene_camera_persist.go), armed
	// by EnableViewpointPersist after the startup seed. nil until armed (old path / tests).
	vpPersist *viewpointPersister
	// posPersist / anchorPersist are the debounced disk persisters for the two FSM-applied
	// edits (node-drag position, ring-move anchor). Armed by EnableEditPersist after the
	// startup seed; nil until armed (tests that never arm).
	posPersist      *nodePosPersister
	anchorPersist   *anchorPersister
	overlaysPersist *overlaysPersister
	// quantOffsetPersist is the debounced disk persister for a node's scalar triple
	// (iTheta,iPhi,iR) about the scene center (quant_offset_persist.go) — the sole
	// persisted position source under the flat polar model. Armed by EnableEditPersist;
	// scheduled from RootMove for the dragged node.
	quantOffsetPersist *quantOffsetPersister
	// spherePersist is the debounced disk persister for the scene sphere (sphere_layout.go
	// md.sceneSphere), armed by EnableEditPersist. Its DEBOUNCE has no caller by design: the
	// sphere is "established once and never moves" (MODEL.md), so there is nothing to coalesce.
	// It is only ever flushed — by LoadSceneSphere on a content-fit, and by handleSaveMsg.
	// nil until armed (tests that never arm).
	spherePersist *sceneSpherePersister
	// selected is the CURRENTLY-SELECTED node id (click-select), owned by Go. "" = nothing
	// selected. Set by the gesture FSM's click outcome (applySelect) and emitted via
	// KindSelect so the buffer snapshot marks the node's Selected column.
	selected string
	// selectedEdge is the CURRENTLY-SELECTED edge label (click-select), owned by Go. "" =
	// no edge selected. Set by the gesture FSM's click outcome (applySelect) and emitted via
	// KindSelect (Edge field) so the buffer snapshot marks the edge's Selected column.
	// Exclusive with `selected`: selecting an edge clears the node selection and vice versa.
	selectedEdge string
	// portRows resolves a numeric buffer PORT-ROW index (carried on a new-system raw hit)
	// back to its (node, port, isInput) identity. Wired to the buffer SnapshotState's
	// port-row table in main.go (new-system only); nil on the old path and in unit tests, in
	// which case the gesture FSM falls back to parsing the legacy port-id string. Go owns the
	// topology and wrote the Port block, so it — not TS — maps a port row to a (node, port).
	portRows PortRowResolver
	// edgeRows resolves a numeric buffer EDGE-ROW index (carried on a new-system raw hit)
	// back to its edge label. Wired to the buffer SnapshotState's edge-row table in main.go
	// (new-system only); nil on the old path and in unit tests, in which case the gesture
	// FSM falls back to the raw hit's Id string. Go owns the topology and wrote the Edge
	// block, so it — not TS — maps an edge row to its label.
	edgeRows EdgeRowResolver
	// nodeRows resolves a numeric buffer NODE-ROW index (carried on a new-system raw hit)
	// back to its node id. Wired to the buffer SnapshotState's node-row table in main.go
	// (new-system only); nil on the old path and in unit tests, in which case the gesture
	// FSM falls back to the raw hit's Id string. Go owns the topology and wrote the Node
	// block, so it — not TS — maps a node row to its id.
	nodeRows NodeRowResolver
	// hoverNode / hoverPort / hoverInput are the CURRENTLY-HOVERED entity (pointer hover),
	// owned by Go. The gesture FSM tracks them from the raycast hit on each pointer-move and
	// emits KindHover ONLY when they change (dedupe) so pointer-move doesn't flood the
	// snapshot. hoverPort != "" means a port is hovered (on hoverNode); otherwise hoverNode
	// (possibly "") is the hovered node. "" / "" = nothing hovered.
	hoverNode  string
	hoverPort  string
	hoverInput bool
	// quantizedLayout gates the quantized absolute-scene-polar snap (quantized_layout.go)
	// — every node is a root, measured/derived about the scene center only.
	quantizedLayout bool
	// layoutHolders resolves a node id to the *LayoutHolder embedded in that node's
	// built struct (reflection-attached by buildNodes the same way LocalPolars
	// itself is attached — see loader.go). This is the ONLY route from the drag
	// path (RootMove) to each node's own LayoutHolder; MoveDispatch does not own
	// or copy LocalPolars itself, it just routes the update to the owning node.
	layoutHolders map[string]*LayoutHolder
	// msgTap is a TEST-ONLY observability seam: when non-nil, sendMove invokes it with
	// every (destID, msg) it routes, BEFORE the send. nil in production — production code
	// never calls SetMsgTap, so msgTap.Load() is always nil there (one atomic load, no
	// lock, no allocation). This exists only so tests can assert the message trace
	// between movers (e.g. the neighborSetC single-hop propagation) is exactly what's
	// expected. It is pure observation — it never authors domain state or changes
	// routing. Stored as an atomic.Pointer (not a plain field) because sendMove is the
	// one chokepoint every mover goroutine calls concurrently.
	msgTap atomic.Pointer[func(destID string, msg moveMsg)]
}

// SetMsgTap installs (or clears, with nil) the test-only message-trace hook. Test-only —
// production code never calls this.
func (md *MoveDispatch) SetMsgTap(tap func(destID string, msg moveMsg)) {
	if tap == nil {
		md.msgTap.Store(nil)
		return
	}
	md.msgTap.Store(&tap)
}

// NodeRowResolver maps a numeric buffer NODE-ROW index to its node id. Implemented by
// Buffer.SnapshotState (which wrote the Node block in this same row order). Kept as an
// interface here so the Wiring package needs no dependency on the Buffer package — main.go
// injects the concrete resolver.
type NodeRowResolver interface {
	LookupNodeRow(row int) (nodeID string, ok bool)
}

// SetNodeRowResolver injects the node-row resolver (new-system only). Called once at
// startup after LoadTopology.
func (md *MoveDispatch) SetNodeRowResolver(r NodeRowResolver) { md.nodeRows = r }

// EdgeRowResolver maps a numeric buffer EDGE-ROW index to its edge label. Implemented by
// Buffer.SnapshotState (which wrote the Edge block in this same row order). Kept as an
// interface here so the Wiring package needs no dependency on the Buffer package — main.go
// injects the concrete resolver.
type EdgeRowResolver interface {
	LookupEdgeRow(row int) (label string, ok bool)
}

// SetEdgeRowResolver injects the edge-row resolver (new-system only). Called once at
// startup after LoadTopology.
func (md *MoveDispatch) SetEdgeRowResolver(r EdgeRowResolver) { md.edgeRows = r }

// PortRowResolver maps a numeric buffer PORT-ROW index to its (node, port, isInput)
// identity. Implemented by Buffer.SnapshotState (which wrote the Port block in this same
// row order). Kept as an interface here so the Wiring package needs no dependency on the
// Buffer package — main.go injects the concrete resolver.
type PortRowResolver interface {
	LookupPortRow(row int) (node, port string, isInput, ok bool)
}

// SetPortRowResolver injects the port-row resolver (new-system only). Called once at
// startup after LoadTopology.
func (md *MoveDispatch) SetPortRowResolver(r PortRowResolver) { md.portRows = r }

// NodeGeomSeed is one node's load-time seed geometry, exported in spec order and consumed
// by main.go's pre-launch tr.NodeGeometry loop (see the row-seeding comment in main.go).
// Ports are the SAME aimed-vs-static port geometry (buildPortGeoms/aimedPortPosDir,
// builders.go) the node's own live emit later produces — computed here from the same
// load-time geoms map, since every node's center is already known at load (buildPartnerCenterFn
// resolves partner centers straight off geoms, no goroutine needed). main.go copies these
// fields into the tr.NodeGeometry call (which additionally resolves the numeric KindID,
// since that table lives in Buffer).
type NodeGeomSeed struct {
	ID, Label, Kind              string
	CX, CY, CZ, Radius, SphereR  float64
	Ports                        []T.PortGeom
	VRX, VRY, VRZ, FRX, FRY, FRZ float64
}

// EdgeGeomSeed is one edge's load-time topology AND its real segment endpoints — the same
// edgeSegment(srcGeom, dstGeom, srcH, dstH) computation the edge's own live recomputeGeometry
// (node_move.go) uses, evaluated here against the load-time geoms so the seed row is never a
// degenerate 0,0,0→0,0,0 segment.
type EdgeGeomSeed struct {
	Label, SrcNode, DstNode string
	SX, SY, SZ, EX, EY, EZ  float64
}

// NodeSeeds returns every node's load-time seed geometry in SPEC ORDER (see
// MoveDispatch.nodeSeeds). Call after LoadTopology returns, before launching any node
// goroutine, and stream each entry via tr.NodeGeometry (main.go).
func (md *MoveDispatch) NodeSeeds() []NodeGeomSeed { return md.nodeSeeds }

// EdgeSeeds returns every edge's load-time seed topology (with real endpoint geometry) in
// SPEC ORDER. Call alongside NodeSeeds; stream each entry via tr.Geometry (main.go).
func (md *MoveDispatch) EdgeSeeds() []EdgeGeomSeed { return md.edgeSeeds }

// newMoveDispatch builds the registry from per-node geometry and per-edge endpoints.
// It creates one nodeMover per node and one edgeMover per edge, registering each in
// the dispatch map under its key (node id / edge id). Outs and dest wires are bound
// later by Bind once node construction has populated them. nodeOrder/edgeOrder are the
// SPEC order (deterministic directory-sorted order, not map iteration order) used to
// build md.nodeSeeds/edgeSeeds for buffer row seeding.
func newMoveDispatch(geoms map[string]nodeGeom, edgeEndpoints map[string]EdgeEndpoints, tr *T.Trace, nodeOrder, edgeOrder []string) *MoveDispatch {
	// nil order (test call sites that don't care about seed order) falls back to sorted
	// map keys — still deterministic, just not necessarily spec order.
	if nodeOrder == nil {
		nodeOrder = make([]string, 0, len(geoms))
		for id := range geoms {
			nodeOrder = append(nodeOrder, id)
		}
		sort.Strings(nodeOrder)
	}
	if edgeOrder == nil {
		edgeOrder = make([]string, 0, len(edgeEndpoints))
		for label := range edgeEndpoints {
			edgeOrder = append(edgeOrder, label)
		}
		sort.Strings(edgeOrder)
	}
	md := &MoveDispatch{
		dispatch:      map[string]chan moveMsg{},
		nodeMovers:    map[string]*nodeMover{},
		edgeMovers:    map[string]*edgeMover{},
		edgeOut:       map[string]*Out{},
		tr:            tr,
		ov:            defaultOverlayState(),
		layoutHolders: map[string]*LayoutHolder{},
	}
	// Static partner-center lookup for the seed pass: every node's center is already known
	// off the load-time geoms map (no goroutine/atomic-snap needed), so this is the SAME
	// buildPartnerCenterFn the dynamic movers use below, just closed over geoms directly
	// instead of md.nodeMovers' atomic snaps. Kept per-node (not shared) to match
	// buildPartnerCenterFn's (nodeID, edgeEndpoints, centerOf) shape.
	seedPartnerCenter := func(nodeID string) partnerCenterFn {
		return buildPartnerCenterFn(nodeID, edgeEndpoints, func(otherID string) vec3 {
			if g, ok := geoms[otherID]; ok {
				return nodeWorldPos(g)
			}
			return vec3{}
		})
	}
	md.nodeSeeds = make([]NodeGeomSeed, 0, len(nodeOrder))
	for _, id := range nodeOrder {
		g, ok := geoms[id]
		if !ok {
			continue
		}
		label := g.Label
		if label == "" {
			label = id
		}
		var cx, cy, cz float64
		if g.HasPos {
			c := nodeWorldPos(g)
			cx, cy, cz = c.X, c.Y, c.Z
		}
		ports := buildPortGeoms(g, aimedPortPosDir(g, seedPartnerCenter(id)))
		md.nodeSeeds = append(md.nodeSeeds, NodeGeomSeed{
			ID: id, Label: label, Kind: g.Kind,
			CX: cx, CY: cy, CZ: cz,
			Radius: nodeRadius(g.Kind), SphereR: effectiveRadius(g),
			Ports: ports,
			VRX:   verticalRingNormalX, VRY: verticalRingNormalY, VRZ: verticalRingNormalZ,
			FRX: flatRingNormalX, FRY: flatRingNormalY, FRZ: flatRingNormalZ,
		})
	}
	md.edgeSeeds = make([]EdgeGeomSeed, 0, len(edgeOrder))
	for _, label := range edgeOrder {
		ep, ok := edgeEndpoints[label]
		if !ok {
			continue
		}
		// Real endpoint geometry: the same edgeSegment computation recomputeGeometry
		// (below) uses on every live move, evaluated once here against the load-time
		// geoms so the seed row is never a degenerate 0,0,0->0,0,0 segment.
		var sx, sy, sz, ex, ey, ez float64
		if srcG, ok := geoms[ep.Source]; ok {
			if dstG, ok := geoms[ep.Target]; ok {
				seg := edgeSegment(srcG, dstG, ep.SourceHandle, ep.TargetHandle)
				sx, sy, sz = seg.Start.X, seg.Start.Y, seg.Start.Z
				ex, ey, ez = seg.End.X, seg.End.Y, seg.End.Z
			}
		}
		md.edgeSeeds = append(md.edgeSeeds, EdgeGeomSeed{
			Label: label, SrcNode: ep.Source, DstNode: ep.Target,
			SX: sx, SY: sy, SZ: sz, EX: ex, EY: ey, EZ: ez,
		})
	}
	for id, g := range geoms {
		nm := newNodeMover(id, g, tr)
		nm.sendMove = md.enqueueFor(nm.outbox)
		nm.centerOf = md.centerOfNode
		nm.commitLocal = md.commitNodeMoveLocal
		nm.neighborSetC = md.neighborSetCRequantize
		md.nodeMovers[id] = nm
		md.dispatch[id] = nm.inbox
	}
	for edgeID, ep := range edgeEndpoints {
		em := newEdgeMover(ep, edgeID, geoms[ep.Source], geoms[ep.Target], tr)
		md.edgeMovers[edgeID] = em
		md.dispatch[edgeID] = em.inbox
	}
	// Wire each nodeMover's aimed-port lookup: for (port,isInput) on nodeID, find its one
	// edge (edgeEndpoints) and read the partner's CURRENT center off the partner
	// nodeMover's atomic snap (md.nodeMovers is a read-only map after this point — only the
	// individual *nodeMover.snap is read cross-goroutine, via the existing atomic pattern).
	for id, nm := range md.nodeMovers {
		nm.partnerCenter = buildPartnerCenterFn(id, edgeEndpoints, func(otherID string) vec3 {
			other, ok := md.nodeMovers[otherID]
			if !ok {
				return vec3{}
			}
			if s := other.snap.Load(); s != nil {
				return s.c
			}
			return vec3{}
		})
	}
	// Give every nodeMover the ids of its OWN incident edges, so a lock-driven move can
	// notify its edges via sendMove (dispatch-map lookup) — no cached channel slice.
	for id, nm := range md.nodeMovers {
		for edgeID, em := range md.edgeMovers {
			if em.srcID == id || em.dstID == id {
				nm.edgeIDs = append(nm.edgeIDs, edgeID)
			}
		}
	}
	return md
}

// Bind wires the per-edge source Outs (keyed "source.sourceHandle" in outSink) and
// dest wires (slotReg, keyed "target.targetHandle") into each edgeMover. Call once
// after node construction.
func (md *MoveDispatch) Bind(outSink map[string]*Out, slotReg SlotRegistry) {
	for edgeID, em := range md.edgeMovers {
		if o, ok := outSink[em.srcID+"."+em.srcH]; ok {
			em.out = o
			md.edgeOut[edgeID] = o
		}
		if pw, ok := slotReg[em.dstID+"."+em.dstH]; ok {
			em.dest = pw
		}
	}
}

// Start launches every mover's goroutine. Each node and each edge drains its own
// inbox until ctx is done — per-goroutine ownership, no central coordinator.
func (md *MoveDispatch) Start(ctx context.Context) {
	for _, nm := range md.nodeMovers {
		go nm.run(ctx)
		// Dedicated sender goroutine for this node's outbound queue (see the outbox
		// type doc comment / cascade-deadlock-fix.md): pops nm's queued messages in
		// FIFO order and performs the actual blocking delivery, so nm's own
		// inbox-drain goroutine (nm.run above) never blocks on a send and therefore
		// always keeps draining its inbox.
		go nm.outbox.run(ctx, md.deliverMove)
	}
	for _, em := range md.edgeMovers {
		go em.run(ctx)
	}
}

// EdgeOut returns the source *Out bound to the given edge label, or nil if unknown.
// Read-only accessor for out-of-package verifiers (the headless cascade reads an
// edge's per-edge in-flight time from the loaded geometry).
func (md *MoveDispatch) EdgeOut(edgeID string) *Out {
	return md.edgeOut[edgeID]
}

// centerOfNode returns the current world center for a node id by loading the
// nodeMover's atomically-published snapshot. Safe to call from any goroutine
// without synchronization — the snap is published via atomic.Pointer after each
// center update so this never races with the mover's live geom writes.
func (md *MoveDispatch) centerOfNode(id string) (vec3, bool) {
	if nm, ok := md.nodeMovers[id]; ok {
		if s := nm.snap.Load(); s != nil {
			return s.c, true
		}
	}
	return vec3{}, false
}

// sendMove routes one moveMsg to another node's (or edge's) inbox by id, if known.
// Used by the decentralized lock-propagation cascade so a nodeMover can re-broadcast
// to its own lock-neighbors without any central worklist — the dispatch map is the
// only "directory" involved, and it's read-only lookup, not a queue.
func (md *MoveDispatch) sendMove(id string, msg moveMsg) {
	if tap := md.msgTap.Load(); tap != nil {
		(*tap)(id, msg)
	}
	if ch, ok := md.dispatch[id]; ok {
		ch <- msg
	}
}

// enqueueFor returns the ENQUEUE half of the per-mover outbound-queue split
// (cascade-deadlock-fix.md): it fires the msgTap (at enqueue time, so tap-based tests'
// counts/ordering match today's synchronous-send behavior) and then appends to ob —
// never blocking. Bound once per node at construction (nm.sendMove = md.enqueueFor(nm.outbox))
// so every send a nodeMover's own handle performs — including the ones
// fanEdgesAndPartners makes on that node's behalf — goes through this node's own
// outbox, never a raw blocking channel write.
func (md *MoveDispatch) enqueueFor(ob *outbox) func(id string, msg moveMsg) {
	return func(id string, msg moveMsg) {
		if tap := md.msgTap.Load(); tap != nil {
			(*tap)(id, msg)
		}
		ob.enqueue(id, msg)
	}
}

// enqueueFuncFor resolves the enqueue closure for nodeID's own outbox (the SELF mover
// whose handler is doing the sending), for use by MoveDispatch methods (fanEdgesAndPartners)
// that are not themselves nodeMover methods. Falls back to the blocking md.sendMove only
// for the (practically unreached in production) case of a nodeID with no live mover —
// there is no outbox to enqueue onto in that case.
func (md *MoveDispatch) enqueueFuncFor(nodeID string) func(id string, msg moveMsg) {
	if nm, ok := md.nodeMovers[nodeID]; ok {
		return nm.sendMove
	}
	return md.sendMove
}

// deliverMove is the DELIVERY half of the per-mover outbound-queue split: the actual
// blocking channel write into the target's inbox, run only from a mover's dedicated
// sender goroutine (outbox.run), never from a handler goroutine. No tap here — the tap
// already fired once, at enqueue (md.enqueueFor), matching the pre-split single-fire
// contract the tap-based tests assert against.
func (md *MoveDispatch) deliverMove(id string, msg moveMsg) {
	if ch, ok := md.dispatch[id]; ok {
		ch <- msg
	}
}

// sendMoveLossy is sendMove's non-blocking twin, reserved for moveMsgKindNeighborSetC
// (requantizeLocalPolars' per-neighbor fan) ONLY. Per MODEL.md there is no
// cross-goroutine delivery guarantee — a goroutine does its own work and drives its
// own outputs, fire-and-forget. Every OTHER sendMove call (drag, center, anchor)
// carries a node's actual position and MUST stay blocking: dropping one of those
// leaves a node's committed geometry stale with no self-heal. A NeighborSetC message
// is different: it exists only to keep a neighbor's cached edge-length fresh, and a
// full inbox means the receiver is already mid-cascade and about to pick up the fresh
// c from its own next commit anyway (or self-heals on reload) — so a drop here is
// redundant, not just tolerable. Blocking would instead risk deadlock: domain
// adjacency is symmetric, so two mutually-adjacent nodes committing concurrently with
// full inboxes could each block sending a set-c to the other while neither is
// draining (handle runs synchronously inside commitLocal), hanging both nodeMover
// goroutines.
func (md *MoveDispatch) sendMoveLossy(id string, msg moveMsg) {
	if tap := md.msgTap.Load(); tap != nil {
		(*tap)(id, msg)
	}
	if ch, ok := md.dispatch[id]; ok {
		select {
		case ch <- msg:
		default:
			// Inbox full: receiver is mid-cascade and will self-requantize on its own
			// next commit (or self-heals on reload) — dropping is safe, not just tolerated.
			if md.tr != nil {
				md.tr.Breadcrumb("requantize.drop", id, "", msg.Kind)
			}
		}
	}
}

// heldCenters / heldEdges snapshot the movers' current geometry.
// heldCenters reads the atomically-published snap (not the live geom) so it is
// safe to call from the stdin goroutine while mover goroutines write their centers.
func (md *MoveDispatch) heldCenters() map[string]vec3 {
	centers := make(map[string]vec3, len(md.nodeMovers))
	for id, m := range md.nodeMovers {
		if s := m.snap.Load(); s != nil {
			centers[id] = s.c
		}
	}
	return centers
}

// heldPolar snapshots every mover's current SCENE POLAR (r,θ,φ about the scene center) from
// the atomically-published snap — the polar source of truth for geometry math (reach, arc,
// colinearity), read safe from the stdin goroutine while movers write their positions.
func (md *MoveDispatch) heldPolar() map[string]polar {
	polars := make(map[string]polar, len(md.nodeMovers))
	for id, m := range md.nodeMovers {
		if s := m.snap.Load(); s != nil {
			polars[id] = s.p
		}
	}
	return polars
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
	// enqueue (the caller's own outbox — see enqueueFuncFor) defers the actual blocking
	// delivery to that mover's dedicated sender goroutine (cascade-deadlock-fix.md), so
	// this call — made from inside handle via commitLocal — never blocks. The
	// dispatch-existence check moved into deliverMove (the sender's eventual blocking
	// write), matching enqueue's other call sites (m.sendMove), which already
	// tap/enqueue unconditionally regardless of whether id resolves.
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
	// trick. emitGeometry reads m.partnerCenter at emit time, which is the moved node's
	// FRESH atomic snap (already written above), so the partner's aimed port marker
	// picks up the new target direction. This does NOT run for torus-locked ports only —
	// it runs for every aimed connected port unconditionally, even if that breaks a
	// port∈torus lock; that is intended.
	partners := map[string]bool{}
	for _, em := range md.edgeMovers {
		if _, moved := newCenters[em.srcID]; moved {
			if _, alsoMoved := newCenters[em.dstID]; !alsoMoved {
				partners[em.dstID] = true
			}
		}
		if _, moved := newCenters[em.dstID]; moved {
			if _, alsoMoved := newCenters[em.srcID]; !alsoMoved {
				partners[em.srcID] = true
			}
		}
	}
	for partnerID := range partners {
		if _, ok := md.nodeMovers[partnerID]; !ok {
			continue
		}
		// Center is deliberately nil (see the doc comment above): this is a PURE
		// re-emit, not a position write. Sending a non-nil Center rebuilt from
		// nm.snap.Load() here would re-apply partnerID's OWN current snapshot to
		// itself — normally an idempotent no-op, but a genuine hazard when a
		// second, concurrently-in-flight fanCenters call (e.g. the node-2→5
		// drag-equalize cascade) has ALREADY queued partnerID's real new-position
		// message on this same inbox: a stale non-nil re-read here would queue
		// BEHIND the real update and clobber it back to the pre-move position on
		// drain. nodeMover.handle's nil-Center branch re-emits from the mover's own
		// live geom (whatever it is by the time this drains), so it can never race
		// or clobber a pending position write. Per-target FIFO order (single sender
		// goroutine per mover) preserves this ordering guarantee even now that
		// delivery is deferred through the outbox.
		enqueue(partnerID, moveMsg{Kind: moveMsgKindCenter, NodeID: partnerID, Center: nil})
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
func (md *MoveDispatch) neighborSetCRequantize(selfID, fromID string, fromCenter vec3) {
	lh, ok := md.layoutHolders[selfID]
	if !ok {
		return
	}
	selfCenter, ok := md.centerOfNode(selfID)
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
			fmt.Sprintf("peer=%s peerCenter=(%.3f,%.3f,%.3f) abc=(%d,%d,%d)",
				fromID, fromCenter.X, fromCenter.Y, fromCenter.Z, it, ip, ir))
		// Routed counterpart of the breadcrumb above: marks selfID in the buffer so the
		// in-editor overlay log lists it (the breadcrumb alone never reaches the buffer).
		md.tr.AbcDrag(selfID)
	}

	if md.quantOffsetPersist != nil {
		if root := md.quantOffsetPersist.root; root != "" {
			if err := WriteLocalPolars(root, selfID, lh.LocalPolarsSnapshot(), lh.Pole()); err != nil {
				logPersistErr("local_polar_persist", selfID, err)
			}
		}
	}
}

// commitNodeMoveLocal is the OWNER-GOROUTINE single-node commit path
// (generalized to every node): used when the commit
// originates on nodeID's OWN mover goroutine (its own inbox handler for a
// moveMsgKindDrag). It publishes nodeID's OWN snap
// SYNCHRONOUSLY via applyCenter — safe and correct here because applyCenter's doc
// contract is "called only from this nodeMover's own inbox-drain goroutine", which
// this is. Also fans centers to incident edges/partners, persists the per-node
// quantized-offset (nodeMover.quantOffset — never a shared map, so no other mover
// goroutine's commit can race this write even for a different node id), and
// requantizes nodeID's local-polar double-links against its (unmoved) neighbors.
func (md *MoveDispatch) commitNodeMoveLocal(nodeID string, newPos vec3) {
	edges := md.heldEdges()
	polars := md.heldPolar()
	polars[nodeID] = cart2polar(newPos.sub(md.sceneSphere.Center))
	reach := reachRFromPolar(polars, edges)

	nm, ok := md.nodeMovers[nodeID]
	if ok {
		nm.applyCenter(newPos, reach[nodeID])
	}
	md.fanEdgesAndPartners(map[string]vec3{nodeID: newPos}, md.enqueueFuncFor(nodeID))

	if md.quantizedLayout && ok {
		off := measureScalar(newPos, md.sceneSphere.Center, nm.quantOffset)
		nm.quantOffset = off
		if md.quantOffsetPersist != nil {
			md.quantOffsetPersist.schedule(nodeID, off, cart2polar(newPos.sub(md.sceneSphere.Center)))
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
// requantizeLocalPolars). The node's center is fanned out (re-emitting its own +
// incident edges' geometry, with the fresh reach radius), its scene-center scalar
// triple is remeasured + persisted (still the reload/render position source), and every
// affected double-link's local polars are re-quantized on BOTH ends. Returns false for
// an unknown node.
func (md *MoveDispatch) RootMove(nodeID string, target vec3) bool {
	// Re-scope the in-editor drag-log to THIS drag: emit the reset BEFORE routing the
	// drag message, so it is ordered ahead of the neighborSetC fan's AbcDrag marks
	// (which land asynchronously on each recipient's own goroutine, after this send).
	if md.tr != nil {
		md.tr.AbcDragReset()
	}
	// EVERY node's drag is a FREE move on the decentralized goroutine-message path —
	// no equal-radii solve, no rule/gate/anchor cascade. The dragged node commits its
	// own new position, then requantizeLocalPolars fans a single neighborSetC
	// assignment to every direct domain neighbor.
	return md.rootMoveViaMessages(nodeID, target)
}

// rootMoveViaMessages is the decentralized drag entry, widened to EVERY node (the
// generalization that came with the quantizedOffsets data-race fix): dragging any node
// no longer commits on the stdin reader's own goroutine — it routes a single
// moveMsgKindDrag to the dragged node's OWN inbox and returns. The dragged node's own
// moveMsgKindDrag handler (nodeMover.handle) does the rest, entirely on its own
// goroutine: commit its own new position (commitLocal — fan + persist + requantize,
// no cross-goroutine self-send). commitLocal's requantizeLocalPolars then sends every
// direct domain neighbor a single moveMsgKindNeighborSetC assignment (see that
// function's doc comment) — there is no equal-radii solve, no rule-node cascade, no
// gate-anchor fan-out; a drag never touches any node's position but its own.
func (md *MoveDispatch) rootMoveViaMessages(nodeID string, target vec3) bool {
	if _, ok := md.nodeMovers[nodeID]; !ok {
		return false
	}
	// Route the drag itself to the dragged node's OWN inbox instead of committing on
	// the stdin reader's goroutine — every node's moveMsgKindDrag handler commits
	// (synchronous local snap publish) on its own goroutine. No central commit call
	// here.
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
	if md.quantOffsetPersist != nil {
		root = md.quantOffsetPersist.root
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
	// just one at a time, so a kick from one offset is checked against every other.
	updatesX := map[string]vec3{}
	for m := range neighbors {
		cM, ok := md.centerOfNode(m)
		if !ok {
			continue
		}
		updatesX[m] = cM.sub(newPos)
	}
	if len(updatesX) == 0 {
		return
	}
	md.requantizePoleTraced(lhX, updatesX)
	writePersist(nodeID, lhX)

	// X tells EVERY direct domain neighbor M its NEW c (the quantized edge radius X just
	// requantized to M above) as a SINGLE ASSIGNMENT — moveMsgKindNeighborSetC. M keeps
	// its OWN stored bearing (QuantITheta/QuantIPhi) to X and repositions itself at the
	// new distance along that same stored direction, X held fixed; M does NOT
	// re-derive its bearing from a live offset and does NOT forward beyond this one
	// hop (neighborSetCReposition). Routed as a message to M's OWN inbox instead of
	// reaching into M's LayoutHolder from X's (this) goroutine — each M's holder and
	// center are written only by M's own goroutine. Sent via sendMoveLossy
	// (non-blocking): a full M inbox means M is already mid-cascade and will pick up
	// X's fresh c on its own next commit, so the drop is safe — and blocking here
	// risks deadlock against M's own symmetric send back to X when both commit
	// concurrently with full inboxes. Unconditional for every neighbor — there is no
	// rule/gate/anchor cascade left to defer to.
	lpByTo := map[string]LocalPolar{}
	for _, lp := range lhX.LocalPolarsSnapshot() {
		lpByTo[lp.To] = lp
	}
	for m := range updatesX {
		if _, ok := lpByTo[m]; !ok {
			continue
		}
		md.sendMoveLossy(m, moveMsg{Kind: moveMsgKindNeighborSetC, NodeID: m, SenderID: nodeID, FromCenter: newPos})
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

// NodeKind returns the kind string for the given node id, or "" if unknown.
// Used by applyEdit to resolve the node's kind when snapping a port-anchor
// world-space direction to the nearest ring-anchor index.
func (md *MoveDispatch) NodeKind(nodeID string) string {
	if nm, ok := md.nodeMovers[nodeID]; ok {
		return nm.geom.Kind
	}
	return ""
}

// Camera viewpoint API — thin delegators to the owned viewpointState (viewpoint_state.go).
// The public signatures are unchanged; the state and behavior live on md.vp.

func (md *MoveDispatch) SetViewpoint(pivot vec3, r float64, pos, up dir) {
	md.vp.SetViewpoint(pivot, r, pos, up)
}
func (md *MoveDispatch) EmitViewpoint(tr *T.Trace)                { md.vp.EmitViewpoint(tr) }
func (md *MoveDispatch) OrbitViewpoint(from, to dir, tr *T.Trace) { md.vp.OrbitViewpoint(from, to, tr) }
func (md *MoveDispatch) OrbitLockedViewpoint(from, to dir, tr *T.Trace) {
	md.vp.OrbitLockedViewpoint(from, to, tr)
}
func (md *MoveDispatch) ZoomViewpoint(factor float64, tr *T.Trace) { md.vp.ZoomViewpoint(factor, tr) }
func (md *MoveDispatch) PanViewpoint(delta vec3, tr *T.Trace) {
	// A dolly is a pure CAMERA move (the eye translates toward the cursor). It must NOT move the
	// scene sphere: coupling them left md.sceneSphere.Center diverged from the movers' held
	// center until a later broadcast reconciled it with a jump (the "zoom got canceled"
	// symptom). Nothing moves the sphere — MODEL.md: "It is established once and never moves."
	// Pan-moves-the-sphere is REJECTED doctrine, not a gap to fill; if it is ever revisited it
	// must be its own gesture, never a side effect of a camera move.
	md.vp.PanViewpoint(delta, tr)
}

// flushPendingPersists synchronously flushes every debounced persister's pending value on
// clean process shutdown (RunStdinReader's stdin-EOF/channel-close return paths). Without
// this, a drag/gesture that lands within the 250ms debounce window of exit is silently
// abandoned — the write never happens and the edit reverts on the next load. Each persister
// is nil-guarded (some may be unarmed in tests/headless runs); posPersist is an inert stub
// (writes nothing) and is intentionally skipped.
func (md *MoveDispatch) flushPendingPersists() {
	if md == nil {
		return
	}
	md.quantOffsetPersist.flushPending()
	md.anchorPersist.flushPending()
	md.vpPersist.flushPending()
	md.overlaysPersist.flushPending()
	// The scene sphere is established once at load and never moves again (MODEL.md), so its
	// debounce is rarely pending, but flushing it here too is cheap and matches the "save"
	// command's behavior (handleSaveMsg) for completeness.
	if md.spherePersist != nil {
		md.spherePersist.flushNow(md.sceneSphere)
	}
}

// EnableViewpointPersist arms gesture-driven camera persistence: every subsequent
// EmitViewpoint (orbit/zoom/pan/home) debounces a write of the current viewpoint to
// `<topologyPath>/view/scene.json`'s cameraPolar (scene_camera_persist.go). Call AFTER
// SeedInitialViewpoint so the seed's own emit does not write the loaded/default pose back.
// Go owns this write (MODEL.md); the old path persists the camera via its own TS scene-save.
func (md *MoveDispatch) EnableViewpointPersist(topologyPath string) {
	p := &viewpointPersister{path: sceneCameraPath(topologyPath), debounce: viewpointPersistDebounce}
	md.vpPersist = p
	md.vp.persist = p.schedule
}

// EnableEditPersist arms disk persistence for the FSM-applied topology edits:
//   - node-drag (RootMove) → the moved node's x/y/z in <root>/nodes/<id>/meta.json
//   - ring-move (applyRingAnchor) → the port's anchorId in the port json file
//   - overlays (applyUpdate toggle/set) → overlay-visibility keys in view/scene.json
//
// Node-position + anchor persistence needs the per-node/per-port files of the directory-tree
// form; for a monolithic topology.json (no per-node files) their root is "" and those two
// persisters no-op. Call AFTER SeedInitialViewpoint so the seed emits do not write the
// loaded state back.
func (md *MoveDispatch) EnableEditPersist(topologyPath string) {
	// sceneTreeRoot handles both the directory form and the file-inside-tree form (and
	// returns "" for a true monolithic topology with no tree), making the two-form bug
	// class unrepresentable here. Do not hand-roll os.Stat/IsDir — use sceneTreeRoot.
	root := sceneTreeRoot(topologyPath)
	md.posPersist = &nodePosPersister{root: root, debounce: viewpointPersistDebounce}
	md.anchorPersist = &anchorPersister{root: root, debounce: viewpointPersistDebounce}
	md.overlaysPersist = &overlaysPersister{path: sceneCameraPath(topologyPath), debounce: viewpointPersistDebounce}
	md.spherePersist = &sceneSpherePersister{path: sceneCameraPath(topologyPath), debounce: viewpointPersistDebounce}
	md.quantOffsetPersist = &quantOffsetPersister{root: root, debounce: viewpointPersistDebounce}
}

// Overlay-visibility API (MoveDispatch delegators), the overlayState methods, the
// overlayToggles table, defaultOverlayState, and the stdinGuideVisPayload mapper are all
// GENERATED into overlay_gen.go from OVERLAY_FLAG_NAMES (tools/gen-node-defs).
