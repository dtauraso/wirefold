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
//     emits its OWN edge geometry (tr.Geometry), and updates its contribution to the
//     dest port's MaxIncomingSimLatencyMs aggregate (PacedWire.SetIncomingLatency,
//     which recomputes the max over ALL feeding edges).
//
// This reproduces, per-goroutine, exactly what the old central applyNodeMove did in
// one stdin goroutine: same node-geometry emit, same per-edge segment/arc recompute,
// same in-flight revision, same edge-geometry emit, same latency aggregate.

package Wiring

import (
	"context"
	"math"
	"sync"
	"sync/atomic"

	T "github.com/dtauraso/wirefold/Trace"
)

// moveMsgKind discriminates moveMsg payloads.
const (
	// The node-move kind ("move", the zero value "") carries no payload and is a
	// no-op in every mover switch, so it has no constant — the switches simply
	// fall through. The remaining kinds each select a distinct payload.
	moveMsgKindFade    = "fade"
	moveMsgKindAnchor  = "anchor"  // per-port anchor update (drag along the ring)
	moveMsgKindCenter  = "center"  // polar-layout re-propagated world center for one node
	moveMsgKindCenters = "centers" // batched centers for an edge: update both endpoints, recompute ONCE
	moveMsgKindResend  = "resend"  // re-emit this mover's held geometry on its own goroutine
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
//   - "fade":       per-wire fade — Faded applied by edgeMover only (nodeMover ignores).
//
// ack (if non-nil) is closed by the mover after it has fully handled the message —
// used only by the synchronous test façade. The live bridge path leaves ack nil.
type moveMsg struct {
	Kind   string
	NodeID string
	Faded  bool
	// Anchor payload (Kind == "anchor"): identify the port whose anchor changed.
	// Port/IsInput name the port on NodeID; AnchorId is the snapped ring-anchor index
	// (Go snaps from the incoming world-space direction; TS never computes the index).
	Port     string
	IsInput  bool
	AnchorId int
	// Center (Kind == "center"): the re-propagated world center for NodeID under the
	// polar layout. Each owning node/edge goroutine writes it onto its held geom
	// and re-emits its own geometry. RootMove updates one node centrally and
	// fans the fresh centers out — the one centralized step (sphere_layout.go).
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
	ack    chan struct{}
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
	// geomMu guards m.geom against the two remaining goroutines that touch it
	// concurrently since SLICE 3: this node's OWN Update() goroutine (applyCenter,
	// the sole writer of position fields) and nodeMover's own inbox-drain goroutine
	// (handle's anchor/resend/default cases, the sole writer of port-anchor fields
	// and the reader for every re-emit). Position is still single-writer by
	// construction (only applyCenter ever writes it); this mutex exists purely so
	// emitGeometry's full-struct read on one goroutine never races a concurrent
	// field write on the other — it is NOT a second position-writer.
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
	// partnerCenter resolves, per (port,isInput) on this node, the CURRENT world center of
	// the single partner node connected via one edge (aimed-port model, port_geometry.go
	// portWorldPosAimed / builders.go partnerCenterFn). Wired by newMoveDispatch from
	// b.edgeEndpoints + the OTHER nodeMover's atomic snap — a dynamic, always-current lookup
	// with no shared mutable state. nil only in tests that build a bare nodeMover directly.
	partnerCenter partnerCenterFn
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
	if msg.Kind == moveMsgKindResend {
		// Re-emit this node's geometry on our own goroutine — the inbox-routed
		// path used by ResendGeometry when movers are running.
		if m.tr != nil {
			m.emitGeometry()
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
			if msg.ack != nil {
				close(msg.ack)
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

// handle applies one inbox message to this edge. For a "fade" message it sets its
// OWN wire's faded flag (the wire owns its flag — no central fan-out). For a move
// message it updates the matching endpoint geom, recomputes the edge's segment + arc,
// writes them onto the source Out, revises any in-flight bead, emits the new edge
// geometry, and updates the dest port's latency aggregate. A move that touches
// neither endpoint is ignored.
func (m *edgeMover) handle(msg moveMsg) {
	if msg.Kind == moveMsgKindFade {
		if m.dest != nil {
			m.dest.SetFaded(msg.Faded)
		}
		return
	}
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
	if msg.Kind == moveMsgKindResend {
		// Re-emit this edge's segment on our own goroutine — the inbox-routed
		// path used by ResendGeometry when movers are running.
		m.emitGeometry()
		return
	}
	// Plain "move" messages have no effect under the polar layout;
	// position updates arrive as "center" messages instead.
	_ = msg
}

// emitGeometry re-emits this edge's segment from its held endpoint geoms without
// touching the source Out, revising in-flight beads, or updating latency aggregates.
// Used by the inbox-routed resend path (ResendGeometry when movers are running) so
// the emit happens on the edge's own goroutine without races on held geom.
func (m *edgeMover) emitGeometry() {
	if m.tr == nil {
		return
	}
	seg := edgeSegment(m.srcGeom, m.dstGeom, m.srcH, m.dstH)
	m.tr.Geometry(m.edgeID, m.srcID, m.dstID,
		seg.Start.X, seg.Start.Y, seg.Start.Z,
		seg.End.X, seg.End.Y, seg.End.Z)
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
		// Update this edge's contribution to the dest port window aggregate; the
		// wire recomputes the max over ALL feeding edges (fan-in safe).
		m.dest.SetIncomingLatency(m.edgeID, lat)
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
			if msg.ack != nil {
				close(msg.ack)
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
	// started is set by Start; the synchronous façade uses the goroutine path when
	// true and direct handler calls otherwise (unit tests that never Start).
	started bool
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
	// posPersist / anchorPersist / fadePersist are the debounced disk persisters for the
	// three FSM-applied edits (node-drag position, ring-move anchor, fade). Armed by
	// EnableEditPersist after the startup seed; nil until armed (tests that never arm).
	posPersist      *nodePosPersister
	anchorPersist   *anchorPersister
	fadePersist     *fadePersister
	overlaysPersist *overlaysPersister
	// quantOffsetPersist is the debounced disk persister for a node's scalar triple
	// (iTheta,iPhi,iR) about the scene center (quant_offset_persist.go) — the sole
	// persisted position source under the flat polar model. Armed by EnableEditPersist;
	// scheduled from RootMove for the dragged node.
	quantOffsetPersist *quantOffsetPersister
	// spherePersist is the debounced disk persister for the scene sphere (sphere_layout.go
	// md.sceneSphere), armed by EnableEditPersist. Scheduled from PanScene on every
	// camera pan. nil until armed (tests that never arm).
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
	// directlyFadedNodes / directlyFadedEdges are the Go-owned fade SEED sets: the node ids
	// and edge labels the user has DIRECTLY toggled faded (pressing "f" on a selection). Go
	// owns fade because it owns topology + selection. ToggleFadeSelection flips the currently-
	// selected entity's membership and emits the full seeds via KindFade; the buffer snapshot
	// mirrors them and recomputes the fade fixpoint (computeFade) each build. Fade STATE is
	// in-memory only — persistence to disk is a separate (pending) batch.
	directlyFadedNodes map[string]bool
	directlyFadedEdges map[string]bool
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
	// quantizedOffsets is the per-node quantized polar offset about the scene center
	// (quantized_layout.go quantizedOffset), keyed by node id. Populated by
	// loader.go computeQuantizedLayout at load time and authoritative from then on —
	// RootMove (drag) remeasures the dragged node's own triple.
	quantizedOffsets map[string]quantizedOffset
	// layoutHolders resolves a node id to the *LayoutHolder embedded in that node's
	// built struct (reflection-attached by buildNodes the same way LocalPolars
	// itself is attached — see loader.go). This is the ONLY route from the drag
	// path (RootMove) to each node's own LayoutHolder; MoveDispatch does not own
	// or copy LocalPolars itself, it just routes the update to the owning node.
	layoutHolders map[string]*LayoutHolder
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

// newMoveDispatch builds the registry from per-node geometry and per-edge endpoints.
// It creates one nodeMover per node and one edgeMover per edge, registering each in
// the dispatch map under its key (node id / edge id). Outs and dest wires are bound
// later by Bind once node construction has populated them.
func newMoveDispatch(geoms map[string]nodeGeom, edgeEndpoints map[string]EdgeEndpoints, tr *T.Trace) *MoveDispatch {
	md := &MoveDispatch{
		dispatch:           map[string]chan moveMsg{},
		nodeMovers:         map[string]*nodeMover{},
		edgeMovers:         map[string]*edgeMover{},
		edgeOut:            map[string]*Out{},
		tr:                 tr,
		ov:                 defaultOverlayState(),
		directlyFadedNodes: map[string]bool{},
		directlyFadedEdges: map[string]bool{},
		layoutHolders:      map[string]*LayoutHolder{},
	}
	for id, g := range geoms {
		nm := newNodeMover(id, g, tr)
		nm.sendMove = md.sendMove
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
// dest wires (slotReg, keyed "target.targetHandle") into each edgeMover, and seeds
// each dest wire's per-edge latency so MaxIncomingSimLatencyMs starts as the max over
// all feeding edges. Call once after node construction.
func (md *MoveDispatch) Bind(outSink map[string]*Out, slotReg SlotRegistry) {
	for edgeID, em := range md.edgeMovers {
		if o, ok := outSink[em.srcID+"."+em.srcH]; ok {
			em.out = o
			md.edgeOut[edgeID] = o
		}
		if pw, ok := slotReg[em.dstID+"."+em.dstH]; ok {
			em.dest = pw
			if em.out != nil {
				pw.SetIncomingLatency(edgeID, em.out.Geom().SimLatencyMs)
			}
		}
	}
}

// Start launches every mover's goroutine. Each node and each edge drains its own
// inbox until ctx is done — per-goroutine ownership, no central coordinator.
func (md *MoveDispatch) Start(ctx context.Context) {
	md.started = true
	for _, nm := range md.nodeMovers {
		go nm.run(ctx)
	}
	for _, em := range md.edgeMovers {
		go em.run(ctx)
	}
}

// ResendGeometry re-emits the full current geometry from the movers' held
// authoritative state: each node's node-geometry (emitNodeGeometry) and each edge's
// segment (tr.Geometry), recomputed from the edge's held endpoint geoms/handles. This
// reproduces exactly what a fresh load streams on startup, so a freshly-(re)mounted
// webview that lost its module-level edge-geometry store can rebuild it without
// restarting Go.
//
// When movers are running (md.started), each mover emits its own geometry on its own
// goroutine via a synchronous moveMsgKindResend inbox message (ack-gated), removing
// all cross-goroutine reads of mover geom. When movers are not started (test setup
// that never calls Start), the direct read path is safe — no concurrent goroutines
// own the geom yet.
func (md *MoveDispatch) ResendGeometry(ctx context.Context, tr *T.Trace) {
	if tr == nil {
		return
	}
	if !md.started {
		// Movers not running — direct read is safe (no concurrent goroutines own geom).
		for _, nm := range md.nodeMovers {
			emitNodeGeometryLocked(tr, nm.id, nm.geom, nm.partnerCenter)
		}
		for _, em := range md.edgeMovers {
			seg := edgeSegment(em.srcGeom, em.dstGeom, em.srcH, em.dstH)
			tr.Geometry(em.edgeID, em.srcID, em.dstID,
				seg.Start.X, seg.Start.Y, seg.Start.Z,
				seg.End.X, seg.End.Y, seg.End.Z)
		}
		return
	}
	// Movers running — route resend through each mover's inbox so the emit happens
	// on the owning goroutine (no cross-goroutine geom reads). Collect all acks
	// before returning so the caller observes a complete geometry stream.
	total := len(md.nodeMovers) + len(md.edgeMovers)
	acks := make([]chan struct{}, 0, total)
	for _, nm := range md.nodeMovers {
		ack := make(chan struct{}) // chan-name-ok: resend-handled acknowledgment sync signal, not a node wire
		select {
		case nm.inbox <- moveMsg{Kind: moveMsgKindResend, ack: ack}:
			acks = append(acks, ack)
		case <-ctx.Done():
			return
		}
	}
	for _, em := range md.edgeMovers {
		ack := make(chan struct{}) // chan-name-ok: resend-handled acknowledgment sync signal, not a node wire
		select {
		case em.inbox <- moveMsg{Kind: moveMsgKindResend, ack: ack}:
			acks = append(acks, ack)
		case <-ctx.Done():
			return
		}
	}
	for _, ack := range acks {
		select {
		case <-ack:
		case <-ctx.Done():
			return
		}
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
	if ch, ok := md.dispatch[id]; ok {
		ch <- msg
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

// fanCenters pushes one "center" message per node (carrying its new world center and
// reach radius) to that node's mover AND every incident edge's mover. Each owning
// goroutine writes the center onto its own held geom and re-emits — the whole-graph
// SOLVE is central; the per-mover APPLY stays decentralized.
//
// Aimed-port re-emit: when an aimed registry is installed, any nodeMover whose
// port aims AT a moved node must also re-emit its geometry so the port direction
// updates to the new target position. We send a no-center moveMsg to those nodes
// so they re-emit with the fresh target center (which the target mover just wrote
// to its geom; the aimer reads via centerOfNode at emit time).
func (md *MoveDispatch) fanCenters(newCenters map[string]vec3, reach map[string]float64) {
	// Per-node DIRECT position writes: route each moved node's own new center to its
	// own nodeMover inbox as a moveMsgKindCenter message carrying Center — nodeMover's
	// handle applies it via applyCenter, making that node's own mover goroutine the
	// sole writer of its position. One per moved node.
	for id, c := range newCenters {
		if ch, ok := md.dispatch[id]; ok {
			cc := c
			ch <- moveMsg{Kind: moveMsgKindCenter, NodeID: id, Center: &cc, ReachR: reach[id]}
		}
	}

	// Per-edge: send ONE batched message carrying every moved endpoint of that edge,
	// so an edge whose both endpoints moved this frame recomputes/emits exactly once.
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
		if ch, ok := md.dispatch[edgeID]; ok {
			ch <- moveMsg{Kind: moveMsgKindCenters, Centers: eps}
		}
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
		if ch, ok := md.dispatch[partnerID]; ok {
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
			// or clobber a pending position write.
			ch <- moveMsg{Kind: moveMsgKindCenter, NodeID: partnerID, Center: nil}
		}
	}
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
	return md.rootMove(nodeID, target, true, nil)
}

// rootMove is RootMove's internal implementation, parameterized by:
//
//   - cascadeToSource: when true and nodeID is node "2", after equalizing node 2's
//     OWN peers, rootMove also re-runs the equalize on its source node "5" at 5's
//     CURRENT (unchanged) position — "5 acts like it was dragged" so 5's own peer
//     distances (5↔7 / 5↔8) recompute against the NEW 2↔5 distance. The cascade
//     call passes cascadeToSource=false so node 5's own equalize does not itself
//     cascade back — one level only, no infinite recursion.
//   - sourceCenterOverride: when non-nil, equalizeNeighborDistances uses this
//     value as the equalize SOURCE's center instead of reading it back off
//     md.centerOfNode(source). This matters ONLY for the cascade call: fanCenters
//     publishes a moved node's new center to its own mover's inbox
//     ASYNCHRONOUSLY (a channel send drained by that mover's own goroutine, which
//     then atomically stores the snapshot — see nodeMover.applyCenter). The
//     cascade calls rootMove(S, ...) synchronously, in the SAME call stack as the
//     nodeID move that just fanned nodeID's fresh center, so centerOfNode(nodeID)
//     read from S's nested equalize could race the not-yet-drained inbox message
//     and observe nodeID's STALE center. Every non-cascaded RootMove call already
//     avoids this exact race for the DRAGGED node itself by using the newPos
//     parameter directly rather than calling centerOfNode(nodeID) after fanning
//     it (see the existing dist computation in equalizeNeighborDistances); the
//     override generalizes that same "pass the fresh value, don't read it back"
//     rule to the one-hop cascade, where the fresh value belongs to a node OTHER
//     than the one rootMove is currently dragging.
func (md *MoveDispatch) rootMove(nodeID string, target vec3, cascadeToSource bool, sourceCenterOverride *vec3) bool {
	if _, ok := md.nodeMovers[nodeID]; !ok {
		return false
	}
	edges := md.heldEdges()

	// The dragged node's new world position is the drag target — continuous, no
	// scene-grid snap. Only the scene-center scalar triple (remeasured below) and
	// each neighbor's local polar (requantizeLocalPolars) are quantized; the
	// position itself never is.
	newPos := target

	emit := map[string]vec3{nodeID: newPos}
	polars := md.heldPolar()
	polars[nodeID] = cart2polar(newPos.sub(md.sceneSphere.Center))
	reach := reachRFromPolar(polars, edges)
	md.fanCenters(emit, reach)

	// Persist the dragged node: the EXACT scene-polar position is the lossless source of
	// truth (loaded verbatim on reload); the quantized triple rides along as a cache.
	if md.quantizedLayout {
		off := measureScalars(map[string]vec3{nodeID: newPos}, map[string]bool{nodeID: true}, md.sceneSphere.Center, md.quantizedOffsets)[nodeID]
		md.quantizedOffsets[nodeID] = off
		if md.quantOffsetPersist != nil {
			md.quantOffsetPersist.schedule(nodeID, off, cart2polar(newPos.sub(md.sceneSphere.Center)))
		}
	}

	md.requantizeLocalPolars(nodeID, newPos)

	// Scoped to nodes 5 and 2 by request: a peer-frame local-polar-radial equalization,
	// NOT a parent/child cascade. The dragged node's double-link distances to its other
	// peers are set equal to its double-link distance to the named source peer (all
	// measured in the dragged node's own frame, dragged node as center); the source peer
	// stays put. Nodes 5 and 2 are mirror sources for each other (5's source is 2, 2's
	// source is 5) and both are HoldNewSendOld — same kind, same all-peers rule.
	switch nodeID {
	case "5":
		md.equalizeNeighborDistancesWithSourceCenter(nodeID, "2", newPos, sourceCenterOverride)
	case "2":
		md.equalizeNeighborDistancesWithSourceCenter(nodeID, "5", newPos, sourceCenterOverride)
		// Cascade: have the source node 5 act like it was dragged too, so ITS other peer
		// distances (5↔7 / 5↔8) recompute against the new 2↔5 distance. Re-run node 5's
		// own rootMove at 5's CURRENT (unchanged) position, passing node 2's just-computed
		// newPos as the sourceCenterOverride so 5's nested equalize reads the FRESH node-2
		// center rather than racing fanCenters' async publication (see rootMove's doc
		// comment). One level only (cascadeToSource=false on the nested call).
		if cascadeToSource {
			if srcCenter, ok := md.centerOfNode("5"); ok {
				fresh := newPos
				md.rootMove("5", srcCenter, false, &fresh)
			}
		}
	}
	return true
}

// equalizeNeighborDistancesWithSourceCenter sets the dragged node's double-link distance to
// every OTHER domain peer (derived from md.edgeMovers, excluding source) equal to its
// double-link distance to the named source peer — a peer operation in the dragged node's own
// local-polar frame, not a parent/child cascade. Each other peer repositions to that distance
// along its CURRENT bearing from the dragged node (direction preserved, radius changed);
// the source peer is left untouched. Each repositioned peer's move is applied exactly as
// RootMove applies the dragged node's own move: fanCenters (recompute reach over the
// affected set), the scalar-triple remeasure + quantOffsetPersist schedule, and
// requantizeLocalPolars for that peer.
// sourceCenterOverride, when non-nil, is used as the source peer's center INSTEAD of reading
// md.centerOfNode(source). See rootMove's doc comment for why this matters — it lets the
// one-level node-2→5 cascade hand the source's equalize a just-computed fresh center instead
// of racing fanCenters' async inbox publication.
func (md *MoveDispatch) equalizeNeighborDistancesWithSourceCenter(dragged, source string, newPos vec3, sourceCenterOverride *vec3) {
	var sourceCenter vec3
	if sourceCenterOverride != nil {
		sourceCenter = *sourceCenterOverride
	} else {
		c, ok := md.centerOfNode(source)
		if !ok {
			return
		}
		sourceCenter = c
	}
	dist := cart2polar(sourceCenter.sub(newPos)).R

	peers := map[string]bool{}
	for _, em := range md.edgeMovers {
		var other string
		switch dragged {
		case em.srcID:
			other = em.dstID
		case em.dstID:
			other = em.srcID
		default:
			continue
		}
		if other == "" || other == source {
			continue
		}
		peers[other] = true
	}
	if len(peers) == 0 {
		return
	}

	moved := map[string]vec3{dragged: newPos}
	for p := range peers {
		center, ok := md.centerOfNode(p)
		if !ok {
			continue
		}
		delta := center.sub(newPos)
		if delta.length() == 0 {
			continue
		}
		dir := delta.normalize()
		moved[p] = newPos.add(dir.scale(dist))
	}
	if len(moved) <= 1 {
		return
	}

	edges := md.heldEdges()
	polars := md.heldPolar()
	for id, c := range moved {
		polars[id] = cart2polar(c.sub(md.sceneSphere.Center))
	}
	reach := reachRFromPolar(polars, edges)
	md.fanCenters(moved, reach)

	if md.quantizedLayout {
		ids := map[string]bool{}
		for id := range moved {
			if id == dragged {
				continue
			}
			ids[id] = true
		}
		if len(ids) > 0 {
			offs := measureScalars(moved, ids, md.sceneSphere.Center, md.quantizedOffsets)
			for id, off := range offs {
				md.quantizedOffsets[id] = off
				if md.quantOffsetPersist != nil {
					md.quantOffsetPersist.schedule(id, off, cart2polar(moved[id].sub(md.sceneSphere.Center)))
				}
			}
		}
	}

	for id, c := range moved {
		if id == dragged {
			continue
		}
		md.requantizeLocalPolars(id, c)
	}
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
	xChanged := false
	for m := range neighbors {
		lhM, okM := md.layoutHolders[m]
		if !okM {
			continue
		}
		cM, ok := md.centerOfNode(m)
		if !ok {
			continue
		}
		xChanged = true

		// X's local polar TO M, on X's own effective step constants.
		tX, pX, rX := lhX.localPolarSteps(m)
		polXtoM := cart2polar(cM.sub(newPos))
		lhX.SetLocalPolar(m,
			int(math.Round(polXtoM.Theta/tX)),
			int(math.Round(polXtoM.Phi/pX)),
			int(math.Round(polXtoM.R/rX)),
			tX, pX, rX)

		// M's local polar TO X, on M's own effective step constants.
		tM, pM, rM := lhM.localPolarSteps(nodeID)
		polMtoX := cart2polar(newPos.sub(cM))
		lhM.SetLocalPolar(nodeID,
			int(math.Round(polMtoX.Theta/tM)),
			int(math.Round(polMtoX.Phi/pM)),
			int(math.Round(polMtoX.R/rM)),
			tM, pM, rM)

		if root != "" {
			if err := WriteLocalPolars(root, m, lhM.LocalPolarsSnapshot()); err != nil {
				logPersistErr("local_polar_persist", m, err)
			}
		}
	}
	if xChanged && root != "" {
		if err := WriteLocalPolars(root, nodeID, lhX.LocalPolarsSnapshot()); err != nil {
			logPersistErr("local_polar_persist", nodeID, err)
		}
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
	// scene sphere — that is the separate PanScene gesture (polar-frame-rewrite.md). Coupling them
	// left md.sceneSphere.Center diverged from the movers' held center until the next PanScene
	// broadcast reconciled it with a jump (the "zoom got canceled" symptom).
	md.vp.PanViewpoint(delta, tr)
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

// EnableEditPersist arms disk persistence for the three FSM-applied topology edits:
//   - node-drag (RootMove) → the moved node's x/y/z in <root>/nodes/<id>/meta.json
//   - ring-move (applyRingAnchor) → the port's anchorId in the port json file
//   - fade (ToggleFadeSelection) → fadedNodes/fadedEdges in view/scene.json
//   - overlays (applyUpdate toggle/set) → overlay-visibility keys in view/scene.json
//
// Node-position + anchor persistence needs the per-node/per-port files of the directory-tree
// form; for a monolithic topology.json (no per-node files) their root is "" and those two
// persisters no-op. Fade rides scene.json, which exists for both forms. Call AFTER
// SeedInitialViewpoint + SeedFade so the seed emits do not write the loaded state back.
func (md *MoveDispatch) EnableEditPersist(topologyPath string) {
	// sceneTreeRoot handles both the directory form and the file-inside-tree form (and
	// returns "" for a true monolithic topology with no tree), making the two-form bug
	// class unrepresentable here. Do not hand-roll os.Stat/IsDir — use sceneTreeRoot.
	root := sceneTreeRoot(topologyPath)
	md.posPersist = &nodePosPersister{root: root, debounce: viewpointPersistDebounce}
	md.anchorPersist = &anchorPersister{root: root, debounce: viewpointPersistDebounce}
	md.fadePersist = &fadePersister{path: sceneCameraPath(topologyPath), debounce: viewpointPersistDebounce}
	md.overlaysPersist = &overlaysPersister{path: sceneCameraPath(topologyPath), debounce: viewpointPersistDebounce}
	md.spherePersist = &sceneSpherePersister{path: sceneCameraPath(topologyPath), debounce: viewpointPersistDebounce}
	md.quantOffsetPersist = &quantOffsetPersister{root: root, debounce: viewpointPersistDebounce}
}

// Overlay-visibility API (MoveDispatch delegators), the overlayState methods, the
// overlayToggles table, defaultOverlayState, and the stdinGuideVisPayload mapper are all
// GENERATED into overlay_gen.go from OVERLAY_FLAG_NAMES (tools/gen-node-defs).
