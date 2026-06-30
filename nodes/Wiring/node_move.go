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
	"fmt"

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
)

// moveMsg is one entry routed to a mover's inbox. kind selects the payload:
//   - "" or "move": node-move — currently a no-op (polar-layout positions all nodes via "center" messages).
//   - "fade":       per-wire fade — Faded applied by edgeMover only (nodeMover ignores).
//
// ack (if non-nil) is closed by the mover after it has fully handled the message —
// used only by the synchronous test façade. The live bridge path leaves ack nil.
type moveMsg struct {
	Kind    string
	NodeID  string
	Faded bool
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
	id       string
	geom     nodeGeom
	inbox    chan moveMsg
	tr       *T.Trace
	// aimed is the registry of ports that dynamically point toward connected node
	// centers. nil when no aimed ports are registered.
	aimed    AimedPortRegistry
	// centerOf returns the current center for a node id — used only when aimed != nil.
	centerOf func(string) (vec3, bool)
}

func newNodeMover(id string, geom nodeGeom, tr *T.Trace) *nodeMover {
	return &nodeMover{id: id, geom: geom, inbox: make(chan moveMsg, 8), tr: tr}
}

// handle applies one move to this node: update held position, re-emit node-geometry.
func (m *nodeMover) handle(msg moveMsg) {
	if msg.NodeID != m.id {
		return
	}
	if msg.Kind == moveMsgKindAnchor {
		// Per-port anchor update: snap to ring-anchor index, mutate this node's held
		// port AnchorId, and re-emit node-geometry so the renderer redraws the port.
		if !setPortAnchorId(&m.geom, msg.Port, msg.IsInput, msg.AnchorId) {
			return
		}
		if m.tr != nil {
			m.emitGeometry()
		}
		return
	}
	if msg.Kind == moveMsgKindCenter {
		// Polar re-propagation: adopt the centrally-computed world center, then
		// re-emit node-geometry.
		m.geom.Center = msg.Center
		m.geom.ReachR = msg.ReachR
		if m.tr != nil {
			m.emitGeometry()
		}
		return
	}
	if m.tr != nil {
		m.emitGeometry()
	}
}

// emitGeometry re-emits this node's authoritative geometry. When an aimed
// registry is installed it uses portDirAimed for registered ports; otherwise
// falls back to the static portDir via emitNodeGeometry.
func (m *nodeMover) emitGeometry() {
	if m.aimed != nil && m.centerOf != nil {
		emitNodeGeometryAimed(m.tr, m.id, m.geom, m.aimed, m.centerOf)
	} else {
		emitNodeGeometry(m.tr, m.id, m.geom)
	}
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
	// aimed / centerOf: same registry as nodeMover; used in recomputeGeometry.
	aimed    AimedPortRegistry
	centerOf func(string) (vec3, bool)
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
		switch msg.NodeID {
		case m.srcID:
			m.srcGeom.Center = msg.Center
		case m.dstID:
			m.dstGeom.Center = msg.Center
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
			cc := c
			m.srcGeom.Center = &cc
			moved = true
		}
		if c, ok := msg.Centers[m.dstID]; ok {
			cc := c
			m.dstGeom.Center = &cc
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
	seg := segmentBetweenPortsAimed(m.srcGeom, m.srcH, m.srcID, m.dstGeom, m.dstH, m.dstID, m.aimed, m.centerOf)
	arc := seg.Start.sub(seg.End).length()
	lat := arc / PulseSpeedWuPerMs

	// Write the new per-edge segment/arc/latency onto the source Out so the next
	// placement uses the new segment (same as old applyNodeMove).
	if m.out != nil {
		m.out.ArcLength = arc
		m.out.SimLatencyMs = lat
		m.out.Start, m.out.End = seg.Start, seg.End
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
		m.tr.Geometry(m.edgeID,
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
	// links is the double-link movement graph (links.go). Polar locks ride on these;
	// the graph is declared at load and is independent of the displayed data edges.
	links []movementLink
	// mirrorLocks are polar mirror locks rebuilt on the link graph (locks.go).
	mirrorLocks []mirrorLock
	// AimedPorts maps (nodeID, portName, isInput) → targetNodeID for ports whose
	// direction should dynamically point toward their connected node's current center.
	// nil when no aimed ports are registered.
	AimedPorts AimedPortRegistry
	// vp is the polar camera viewpoint state. Mutated by SetViewpoint/OrbitViewpoint/
	// ZoomViewpoint/PanViewpoint; emitted via EmitViewpoint. Owned entirely by
	// MoveDispatch — no separate goroutine; callers serialize externally (stdin reader
	// runs in a single goroutine).
	vp viewpoint
	// tr is the trace sink (retained for trace emission; diagnostic breadcrumbs removed).
	tr *T.Trace
	// sceneToriVisible is the current polar-guide tori visibility. true by default
	// (tori shown on startup). Toggled by ToggleSceneTori; emitted via EmitSceneTori.
	sceneToriVisible bool
	// scenePolesVisible is the current scene-center pole frame visibility. true by default.
	// Toggled by ToggleScenePoles; emitted via EmitScenePoles.
	scenePolesVisible bool
	// nodePolesVisible is the current per-node pole frame visibility. true by default.
	// Toggled by ToggleNodePoles; emitted via EmitNodePoles.
	nodePolesVisible bool
	// angleLabelsVisible is the current θ/φ angle arc+label visibility. true by default.
	// Toggled by ToggleAngleLabels; emitted via EmitAngleLabels.
	angleLabelsVisible bool
	// selSpherePolesVisible is the current selection-sphere pole axis visibility. true by default.
	// Toggled by ToggleSelSpherePoles; emitted via EmitSelSpherePoles.
	selSpherePolesVisible bool
	// handholdsVisible is the current rotation-handhold grab-sphere visibility. true by default.
	// Toggled by ToggleHandholds; emitted via EmitHandholds.
	handholdsVisible bool
	// labelsGlobalVisible is the current global node-label visibility. true by default.
	// Toggled by ToggleLabelsGlobal; emitted via EmitLabelsGlobal.
	labelsGlobalVisible bool
	// badgesGlobalVisible is the current global occlusion-badge visibility. true by default.
	// Toggled by ToggleBadgesGlobal; emitted via EmitBadgesGlobal.
	badgesGlobalVisible bool
	// overlaysVisible is the master overlays visibility. true by default (all overlays shown).
	// Toggled by ToggleOverlaysVis; emitted via EmitOverlaysVis.
	overlaysVisible bool
	// doubleLinksVisible is the double-link overlay visibility. false by default (overlay hidden).
	// Toggled by ToggleDoubleLinks; emitted via EmitDoubleLinks.
	doubleLinksVisible bool
}

// newMoveDispatch builds the registry from per-node geometry and per-edge endpoints.
// It creates one nodeMover per node and one edgeMover per edge, registering each in
// the dispatch map under its key (node id / edge id). Outs and dest wires are bound
// later by Bind once node construction has populated them.
func newMoveDispatch(geoms map[string]nodeGeom, edgeEndpoints map[string]EdgeEndpoints, tr *T.Trace) *MoveDispatch {
	md := &MoveDispatch{
		dispatch:         map[string]chan moveMsg{},
		nodeMovers:       map[string]*nodeMover{},
		edgeMovers:       map[string]*edgeMover{},
		edgeOut:          map[string]*Out{},
		tr:               tr,
		sceneToriVisible:      true,
		scenePolesVisible:     true,
		nodePolesVisible:      true,
		angleLabelsVisible:    true,
		selSpherePolesVisible: true,
		handholdsVisible:      true,
		labelsGlobalVisible:   true,
		badgesGlobalVisible:   true,
		overlaysVisible:       true,
	}
	for id, g := range geoms {
		nm := newNodeMover(id, g, tr)
		md.nodeMovers[id] = nm
		md.dispatch[id] = nm.inbox
	}
	for edgeID, ep := range edgeEndpoints {
		em := newEdgeMover(ep, edgeID, geoms[ep.Source], geoms[ep.Target], tr)
		md.edgeMovers[edgeID] = em
		md.dispatch[edgeID] = em.inbox
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
				pw.SetIncomingLatency(edgeID, em.out.SimLatencyMs)
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
// restarting Go. Safe to call repeatedly and while running: it only reads each mover's
// held geom and emits — no inbox writes, no recompute side effects on Outs/wires.
func (md *MoveDispatch) ResendGeometry(tr *T.Trace) {
	if tr == nil {
		return
	}
	centerOf := md.centerOfNode
	for _, nm := range md.nodeMovers {
		if nm.aimed != nil {
			emitNodeGeometryAimed(tr, nm.id, nm.geom, nm.aimed, centerOf)
		} else {
			emitNodeGeometry(tr, nm.id, nm.geom)
		}
	}
	for _, em := range md.edgeMovers {
		seg := segmentBetweenPortsAimed(em.srcGeom, em.srcH, em.srcID, em.dstGeom, em.dstH, em.dstID, em.aimed, centerOf)
		tr.Geometry(em.edgeID,
			seg.Start.X, seg.Start.Y, seg.Start.Z,
			seg.End.X, seg.End.Y, seg.End.Z)
	}
}

// EdgeOut returns the source *Out bound to the given edge label, or nil if unknown.
// Read-only accessor for out-of-package verifiers (the headless cascade reads an
// edge's per-edge in-flight time from the loaded geometry).
func (md *MoveDispatch) EdgeOut(edgeID string) *Out {
	return md.edgeOut[edgeID]
}

// centerOfNode returns the current world center for a node id, reading directly
// from the holding nodeMover. Used as the centerOf closure for portDirAimed.
// Safe to call from any goroutine that already serializes with the mover
// (e.g. during a fanCenters dispatch where the mover hasn't yet received the
// new center — that is fine: the slightly-stale aim is corrected by the
// subsequent re-emit of the aimed node's own geometry once it processes its
// center message).
func (md *MoveDispatch) centerOfNode(id string) (vec3, bool) {
	if nm, ok := md.nodeMovers[id]; ok && nm.geom.Center != nil {
		return *nm.geom.Center, true
	}
	return vec3{}, false
}

// installAimedPorts sets the aimed registry on every nodeMover and edgeMover and
// stores it on md.AimedPorts for ResendGeometry. Call after newMoveDispatch and
// before Start so the closures are in place when goroutines begin.
func (md *MoveDispatch) installAimedPorts(registry AimedPortRegistry) {
	md.AimedPorts = registry
	centerOf := md.centerOfNode
	for _, nm := range md.nodeMovers {
		nm.aimed = registry
		nm.centerOf = centerOf
	}
	for _, em := range md.edgeMovers {
		em.aimed = registry
		em.centerOf = centerOf
	}
}

// heldCenters / heldEdges snapshot the movers' current geometry.
func (md *MoveDispatch) heldCenters() map[string]vec3 {
	centers := make(map[string]vec3, len(md.nodeMovers))
	for id, m := range md.nodeMovers {
		if m.geom.Center != nil {
			centers[id] = *m.geom.Center
		}
	}
	return centers
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
	// Collect set of nodes that need a re-emit due to aimed-port targeting.
	// We track them separately to avoid double-sending to nodes that are already
	// in newCenters (they will re-emit from their own center message).
	aimedReemit := map[string]bool{}

	// Per-node center messages (carry ReachR for sphereR streaming). One per moved node.
	for id, c := range newCenters {
		cc := c
		if ch, ok := md.dispatch[id]; ok {
			ch <- moveMsg{Kind: moveMsgKindCenter, NodeID: id, Center: &cc, ReachR: reach[id]}
		}
		// If an aimed registry is installed, trigger re-emit for any node whose
		// port aims at the node that just moved (reverse-lookup the registry).
		if md.AimedPorts != nil {
			for key, targetID := range md.AimedPorts {
				if targetID != id {
					continue
				}
				// Skip if the aimer is itself in newCenters (it re-emits from its
				// own center message above).
				if _, alreadyMoving := newCenters[key.NodeID]; !alreadyMoving {
					aimedReemit[key.NodeID] = true
				}
			}
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

	// Send a center message to each aimer so it re-emits with the fresh aim.
	// The aimer's own center hasn't changed — we preserve its current Center and
	// ReachR so the nodeMover just re-emits its geometry with the updated aim dir.
	for id := range aimedReemit {
		nm, exists := md.nodeMovers[id]
		if !exists || nm.geom.Center == nil {
			continue
		}
		if ch, ok := md.dispatch[id]; ok {
			cc := *nm.geom.Center
			ch <- moveMsg{Kind: moveMsgKindCenter, NodeID: id, Center: &cc, ReachR: nm.geom.ReachR}
		}
	}
}

// RootMove handles a node-drag under the polar layout
// (docs/planning/visual-editor/polar-coordinate-model.md): the dragged node's
// OUTER POLAR ROOT is the single authority. The world-space target converts to a
// root (about the container origin); only THAT node's root + center change (soft
// membership — no other node moves). Every center the node sits on recomputes its
// reach radius on the fresh positions so its ring grows around the node, and those
// centers are re-emitted (center unchanged, ReachR updated). Returns false for an
// unknown node.
func (md *MoveDispatch) RootMove(nodeID string, target vec3) bool {
	if _, ok := md.nodeMovers[nodeID]; !ok {
		return false
	}
	// Move ONLY the dragged node. There is no lock system and no central position store:
	// node positions live in the movers' held geometry (geom.Center). Fan the new center
	// so the node and the edges touching it re-emit their geometry. Movement constraints
	// (the double-link locks) will be reintroduced here later.
	edges := md.heldEdges()
	emit := map[string]vec3{nodeID: target}

	// pos reads the dragged node's TARGET (from emit) before falling back to the movers'
	// held centers.
	pos := func(id string) (vec3, bool) {
		if w, ok := emit[id]; ok {
			return w, true
		}
		return md.nodeCenter(id)
	}
	// Drag edge: the mouse handed in a world point, so recompute the polar of every link
	// touching the dragged node (the ONE world→polar conversion). Thereafter the locks
	// read the stored link polar — no cart2polar in the lock equation.
	md.refreshLinksTouching(nodeID, pos)
	// Apply the polar locks riding on the link graph: each triggered follower is written
	// (in polar, on its link) and its derived world is merged into the single per-frame fan.
	for id, w := range md.applyMirrorLocks(nodeID, pos) {
		emit[id] = w
	}

	// Recompute every center's reach over the updated positions and fan all movers ONCE.
	centers := md.heldCenters()
	for id, w := range emit {
		centers[id] = w
	}
	reach := reachRFromCenters(centers, edges)
	md.fanCenters(emit, reach)
	return true
}

// reachRFromCenters computes each node's sphere REACH radius (max distance from a
// node's center to any node it outputs to) under the given centers and edge set.
// Mirrors loader.go buildFromSpec; used by RootMove so the fanned "center" message
// carries the new reach radius and the ring stays sized during a drag.
func reachRFromCenters(centers map[string]vec3, edges []sphereEdge) map[string]float64 {
	reachR := map[string]float64{}
	for _, e := range edges {
		sc, okS := centers[e.Source]
		tc, okT := centers[e.Target]
		if !okS || !okT {
			continue
		}
		if d := chordLength(sc, tc); d > reachR[e.Source] {
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

// SetOrigin re-bases the polar frame to newOrigin (camera pan focus), preserving
// every node's world position. It does NOT re-emit node-geometry: reOrigin leaves
// each mover's world Center unchanged, so the renderer already holds correct
// positions — re-emitting identical geometry is pure churn that thrashes the editor
// and jitters camera-derived geometry. tr is unused (kept for call-site stability).
// Call from the stdin reader on op=="set-origin".
func (md *MoveDispatch) SetOrigin(o vec3, _ *T.Trace) {
	// No-op: the polar frame origin lived in the deleted position store. Node positions
	// are the movers' world Centers, which a re-origin never changed anyway.
	_ = o
}

// SetViewpoint installs a known camera state without emitting. Used by the "set"
// viewpoint op to seed the viewpoint from persisted or initial values, followed by
// EmitViewpoint to broadcast it. Also clears any locked rotation axis from a prior
// handhold gesture so the next gesture starts fresh.
func (md *MoveDispatch) SetViewpoint(pivot vec3, r float64, pos, up dir) {
	md.vp.pivot = pivot
	md.vp.r = r
	md.vp.pos = pos
	md.vp.up = up
	md.vp.lockedAxis = nil
}

// EmitViewpoint emits the current camera viewpoint state as a camera trace event.
func (md *MoveDispatch) EmitViewpoint(tr *T.Trace) {
	if tr == nil {
		return
	}
	tr.Camera(md.vp.pivot.X, md.vp.pivot.Y, md.vp.pivot.Z, md.vp.r,
		md.vp.pos.Theta, md.vp.pos.Phi,
		md.vp.up.Theta, md.vp.up.Phi)
}

// OrbitViewpoint applies a great-circle orbit (carrying from→to) and emits the new state.
func (md *MoveDispatch) OrbitViewpoint(from, to dir, tr *T.Trace) {
	md.vp.orbit(from, to)
	md.EmitViewpoint(tr)
}

// OrbitLockedViewpoint applies a handhold-constrained orbit: the first call locks the
// rotation axis from the from→to arc; subsequent calls keep the same axis. The lock is
// cleared by the next SetViewpoint. Emits a camera event each call.
func (md *MoveDispatch) OrbitLockedViewpoint(from, to dir, tr *T.Trace) {
	md.vp.orbitLocked(from, to)
	md.EmitViewpoint(tr)
}

// ZoomViewpoint scales the orbit radius by factor and emits the new state.
func (md *MoveDispatch) ZoomViewpoint(factor float64, tr *T.Trace) {
	md.vp.zoom(factor)
	md.EmitViewpoint(tr)
}

// PanViewpoint slides the orbit pivot by a world delta and emits the new state.
func (md *MoveDispatch) PanViewpoint(delta vec3, tr *T.Trace) {
	md.vp.pan(delta)
	md.EmitViewpoint(tr)
}

// setFlag flips *field and emits the new value via emit. Shared body of the
// uniform Toggle* visibility methods (those that are just flip-then-emit) so each
// stays a single self-documenting line. The two flags that also drop a breadcrumb
// (scene/node poles) keep their bodies inline.
func (md *MoveDispatch) setFlag(field *bool, emit func(bool)) {
	*field = !*field
	emit(*field)
}

// ToggleSceneTori flips the polar-guide tori visibility and emits a scene-tori event.
// Called from applyEdit on op="tori-vis"; fire-and-forget from TS.
func (md *MoveDispatch) ToggleSceneTori(tr *T.Trace) {
	md.setFlag(&md.sceneToriVisible, tr.SceneTori)
}

// EmitSceneTori emits the current tori visibility without toggling it. Use this on
// startup or geometry-resend to seed the renderer's initial state.
func (md *MoveDispatch) EmitSceneTori(tr *T.Trace) {
	tr.SceneTori(md.sceneToriVisible)
}

// ToggleScenePoles flips the scene-center pole frame visibility and emits a scene-poles event.
// Called from applyEdit on op="scene-poles"; fire-and-forget from TS.
func (md *MoveDispatch) ToggleScenePoles(tr *T.Trace) {
	md.scenePolesVisible = !md.scenePolesVisible
	tr.Breadcrumb("pole-toggle-go", "scene", "", fmt.Sprintf("visible=%v", md.scenePolesVisible))
	tr.ScenePoles(md.scenePolesVisible)
}

// EmitScenePoles emits the current scene pole frame visibility without toggling it.
func (md *MoveDispatch) EmitScenePoles(tr *T.Trace) {
	tr.ScenePoles(md.scenePolesVisible)
}

// ToggleNodePoles flips the per-node pole frame visibility and emits a node-poles event.
// Called from applyEdit on op="node-poles"; fire-and-forget from TS.
func (md *MoveDispatch) ToggleNodePoles(tr *T.Trace) {
	md.nodePolesVisible = !md.nodePolesVisible
	tr.Breadcrumb("pole-toggle-go", "nodes", "", fmt.Sprintf("visible=%v", md.nodePolesVisible))
	tr.NodePoles(md.nodePolesVisible)
}

// EmitNodePoles emits the current per-node pole frame visibility without toggling it.
func (md *MoveDispatch) EmitNodePoles(tr *T.Trace) {
	tr.NodePoles(md.nodePolesVisible)
}

// ToggleAngleLabels flips the θ/φ angle arc+label visibility and emits an angle-labels event.
// Called from applyEdit on op="angle-labels"; fire-and-forget from TS.
func (md *MoveDispatch) ToggleAngleLabels(tr *T.Trace) {
	md.setFlag(&md.angleLabelsVisible, tr.AngleLabels)
}

// EmitAngleLabels emits the current angle arc+label visibility without toggling it.
func (md *MoveDispatch) EmitAngleLabels(tr *T.Trace) {
	tr.AngleLabels(md.angleLabelsVisible)
}

// AngleLabels returns the current angle arc+label visibility.
func (md *MoveDispatch) AngleLabels() bool {
	return md.angleLabelsVisible
}

// ToggleSelSpherePoles flips the selection-sphere pole axis visibility and emits a sel-sphere-poles event.
// Called from applyEdit on op="sel-sphere-poles"; fire-and-forget from TS.
func (md *MoveDispatch) ToggleSelSpherePoles(tr *T.Trace) {
	md.setFlag(&md.selSpherePolesVisible, tr.SelSpherePoles)
}

// EmitSelSpherePoles emits the current selection-sphere pole axis visibility without toggling it.
func (md *MoveDispatch) EmitSelSpherePoles(tr *T.Trace) {
	tr.SelSpherePoles(md.selSpherePolesVisible)
}

// ToggleHandholds flips the rotation-handhold grab-sphere visibility and emits a handholds event.
// Called from applyEdit on op="handholds-vis"; fire-and-forget from TS.
func (md *MoveDispatch) ToggleHandholds(tr *T.Trace) {
	md.setFlag(&md.handholdsVisible, tr.Handholds)
}

// EmitHandholds emits the current handhold visibility without toggling it.
func (md *MoveDispatch) EmitHandholds(tr *T.Trace) {
	tr.Handholds(md.handholdsVisible)
}

// ToggleLabelsGlobal flips the global node-label visibility and emits a labels-global event.
// Called from applyEdit on op="labels-vis"; fire-and-forget from TS.
func (md *MoveDispatch) ToggleLabelsGlobal(tr *T.Trace) {
	md.setFlag(&md.labelsGlobalVisible, tr.LabelsGlobal)
}

// EmitLabelsGlobal emits the current global label visibility without toggling it.
func (md *MoveDispatch) EmitLabelsGlobal(tr *T.Trace) {
	tr.LabelsGlobal(md.labelsGlobalVisible)
}

// ToggleBadgesGlobal flips the global occlusion-badge visibility and emits a badges-global event.
// Called from applyEdit on op="badges-vis"; fire-and-forget from TS.
func (md *MoveDispatch) ToggleBadgesGlobal(tr *T.Trace) {
	md.setFlag(&md.badgesGlobalVisible, tr.BadgesGlobal)
}

// EmitBadgesGlobal emits the current global badge visibility without toggling it.
func (md *MoveDispatch) EmitBadgesGlobal(tr *T.Trace) {
	tr.BadgesGlobal(md.badgesGlobalVisible)
}

// ToggleOverlaysVis flips the master overlays visibility and emits an overlays-vis event.
// Called from applyEdit on op="overlays-vis"; fire-and-forget from TS.
func (md *MoveDispatch) ToggleOverlaysVis(tr *T.Trace) {
	md.setFlag(&md.overlaysVisible, tr.OverlaysVis)
}

// EmitOverlaysVis emits the current master overlays visibility without toggling it.
func (md *MoveDispatch) EmitOverlaysVis(tr *T.Trace) {
	tr.OverlaysVis(md.overlaysVisible)
}

// ToggleDoubleLinks flips the double-link overlay visibility and emits a double-links event.
// Called from applyEdit on op="double-links"; fire-and-forget from TS.
func (md *MoveDispatch) ToggleDoubleLinks(tr *T.Trace) {
	md.setFlag(&md.doubleLinksVisible, tr.DoubleLinks)
}

// EmitDoubleLinks emits the current double-link overlay visibility without toggling it.
func (md *MoveDispatch) EmitDoubleLinks(tr *T.Trace) {
	tr.DoubleLinks(md.doubleLinksVisible)
}

// SetGuideVisibility sets all polar-guide visibilities to explicit values (the TS startup
// push so settings survive a Go respawn on window reload) and emits each so the renderer
// reflects them. Set-to-value, unlike the flip-style Toggle* methods.
func (md *MoveDispatch) SetGuideVisibility(tori, scenePoles, nodePoles, angleLabels, selSpherePoles, handholds, doubleLinks, labelsGlobal, badgesGlobal, overlays bool, tr *T.Trace) {
	md.sceneToriVisible = tori
	md.scenePolesVisible = scenePoles
	md.nodePolesVisible = nodePoles
	md.angleLabelsVisible = angleLabels
	md.selSpherePolesVisible = selSpherePoles
	md.handholdsVisible = handholds
	md.doubleLinksVisible = doubleLinks
	md.labelsGlobalVisible = labelsGlobal
	md.badgesGlobalVisible = badgesGlobal
	md.overlaysVisible = overlays
	md.EmitSceneTori(tr)
	md.EmitScenePoles(tr)
	md.EmitNodePoles(tr)
	md.EmitAngleLabels(tr)
	md.EmitSelSpherePoles(tr)
	md.EmitHandholds(tr)
	md.EmitDoubleLinks(tr)
	md.EmitLabelsGlobal(tr)
	md.EmitBadgesGlobal(tr)
	md.EmitOverlaysVis(tr)
}

