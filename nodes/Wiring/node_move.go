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

	T "github.com/dtauraso/wirefold/Trace"
)

// moveMsgKind discriminates moveMsg payloads.
const (
	moveMsgKindMove   = "move" // default (zero-value "" is also treated as move)
	moveMsgKindFade   = "fade"
	moveMsgKindAnchor = "anchor" // per-port anchor update (drag along the ring)
	moveMsgKindCenter = "center" // sphere-chain re-propagated world center for a node
)

// moveMsg is one entry routed to a mover's inbox. kind selects the payload:
//   - "" or "move": node-move — NodeID + Cell applied by nodeMover and edgeMover.
//   - "fade":       per-wire fade — Faded applied by edgeMover only (nodeMover ignores).
//
// ack (if non-nil) is closed by the mover after it has fully handled the message —
// used only by the synchronous test façade. The live bridge path leaves ack nil.
type moveMsg struct {
	Kind    string
	NodeID  string
	// Cell (node-move only): the lattice cell (i,j,k) the incoming world target snapped
	// to (Go snaps in applyEdit via worldToLattice). It is the sole node-position model;
	// nodeWorldPos resolves the center from Cell (latticeToWorld). nil → cell {0,0,0}.
	Cell  *[3]int
	Faded bool
	// Anchor payload (Kind == "anchor"): identify the port whose anchor changed.
	// Port/IsInput name the port on NodeID; AnchorId is the snapped ring-anchor index
	// (Go snaps from the incoming world-space direction; TS never computes the index).
	Port     string
	IsInput  bool
	AnchorId int
	// Center (Kind == "center"): the re-propagated world center for NodeID under
	// sphere-chain layout. Dir is the node's (possibly re-aimed) unit direction on its
	// parent's sphere. Each owning node/edge goroutine writes these onto its held geom
	// (Center overrides the lattice path; Dir keeps a later re-propagation consistent)
	// and re-emits its own geometry. SphereMove computes both centrally and fans them
	// out — the one centralized step (whole-graph placement, sphere_layout.go).
	Center *vec3
	Dir    *[3]float64
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
			emitNodeGeometry(m.tr, m.id, m.geom)
		}
		return
	}
	if msg.Kind == moveMsgKindCenter {
		// Sphere-chain re-propagation: adopt the centrally-computed world center (and
		// the re-aimed Dir for a later re-propagation), then re-emit node-geometry.
		m.geom.Center = msg.Center
		if msg.Dir != nil {
			m.geom.Dir = msg.Dir
		}
		if m.tr != nil {
			emitNodeGeometry(m.tr, m.id, m.geom)
		}
		return
	}
	m.geom.Cell = msg.Cell // lattice snap is the only node-position model
	if m.tr != nil {
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
		// Sphere-chain re-propagation: adopt the centrally-computed center (+ re-aimed
		// Dir) on whichever endpoint this message names, then recompute the edge.
		switch msg.NodeID {
		case m.srcID:
			m.srcGeom.Center = msg.Center
			if msg.Dir != nil {
				m.srcGeom.Dir = msg.Dir
			}
		case m.dstID:
			m.dstGeom.Center = msg.Center
			if msg.Dir != nil {
				m.dstGeom.Dir = msg.Dir
			}
		default:
			return
		}
		m.recomputeGeometry()
		return
	}
	switch msg.NodeID {
	case m.srcID:
		m.srcGeom.Cell = msg.Cell // lattice snap is the only endpoint-position model
	case m.dstID:
		m.dstGeom.Cell = msg.Cell
	default:
		return
	}

	m.recomputeGeometry()
}

// recomputeGeometry re-derives this edge's segment/arc/latency from its held endpoint
// geoms+handles and propagates them: write onto the source Out, revise any in-flight
// bead (fraction-preserving), update the dest port window aggregate, and emit the new
// segment so the renderer redraws the wire. Shared by node-move and port-anchor handling.
func (m *edgeMover) recomputeGeometry() {
	seg := segmentBetweenPorts(m.srcGeom, m.srcH, m.dstGeom, m.dstH)
	arc := arcLengthBetweenPorts(m.srcGeom, m.srcH, m.dstGeom, m.dstH)
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
}

// newMoveDispatch builds the registry from per-node geometry and per-edge endpoints.
// It creates one nodeMover per node and one edgeMover per edge, registering each in
// the dispatch map under its key (node id / edge id). Outs and dest wires are bound
// later by Bind once node construction has populated them.
func newMoveDispatch(geoms map[string]nodeGeom, edgeEndpoints map[string]EdgeEndpoints, tr *T.Trace) *MoveDispatch {
	md := &MoveDispatch{
		dispatch:   map[string]chan moveMsg{},
		nodeMovers: map[string]*nodeMover{},
		edgeMovers: map[string]*edgeMover{},
		edgeOut:    map[string]*Out{},
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
	for _, nm := range md.nodeMovers {
		emitNodeGeometry(tr, nm.id, nm.geom)
	}
	for _, em := range md.edgeMovers {
		seg := segmentBetweenPorts(em.srcGeom, em.srcH, em.dstGeom, em.dstH)
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

// sphereChainActive reports whether sphere-chain layout is in effect: at least one
// held node carries an explicit R (mirrors the GATE in computeSphereChainPositions).
// When false, applyEdit keeps the lattice (Cell) move path.
func (md *MoveDispatch) sphereChainActive() bool {
	for _, nm := range md.nodeMovers {
		if nm.geom.R != nil {
			return true
		}
	}
	return false
}

// SphereMove handles a node-drag under sphere-chain layout: it re-aims the moved
// node's Dir on its PARENT's sphere toward the world-space target, quantized to the
// node's own diameter steps, then RE-PROPAGATES every node's world center from the
// anchor and pushes the fresh centers to every node/edge mover (which re-emit their
// own geometry). Sphere-chain placement is inherently a WHOLE-GRAPH computation
// (sphere_layout.go), so this re-aim + re-propagate cannot be decentralized the way a
// lattice Cell move is — the one place node-position logic is centralized.
//
//   - The anchor (node "1", or a node with no parent) has no parent sphere to sit on;
//     dragging it is a no-op (documented). Future work could translate the whole graph.
//   - target is the world point TS unprojected through the node; newDir =
//     normalize(target - parentCenter), then quantizeDirToStep snaps it.
//
// Returns the re-aimed Dir (for persistence) and true on a real move; nil,false when
// the move was a no-op (unknown node, anchor, or sub-step displacement).
func (md *MoveDispatch) SphereMove(nodeID string, target vec3) (*[3]float64, bool) {
	if _, ok := md.nodeMovers[nodeID]; !ok {
		return nil, false
	}
	// Rebuild the full geom map + undirected edge list from held mover state.
	geoms := make(map[string]nodeGeom, len(md.nodeMovers))
	for id, m := range md.nodeMovers {
		geoms[id] = m.geom
	}
	edges := make([]sphereEdge, 0, len(md.edgeMovers))
	for _, em := range md.edgeMovers {
		edges = append(edges, sphereEdge{Source: em.srcID, Target: em.dstID})
	}

	parents := sphereChainParents(geoms, edges)
	parentID, hasParent := parents[nodeID]
	if !hasParent {
		return nil, false // anchor / unparented node: no parent sphere to re-aim on.
	}

	// Current centers (pre-move) give the parent's world center to aim from.
	centers := computeSphereChainPositions(geoms, edges)
	parentCenter, okP := centers[parentID]
	if !okP {
		return nil, false
	}

	newDir := target.sub(parentCenter)
	if newDir.length() == 0 {
		return nil, false
	}
	step := diameterStepAngle(nodeR(geoms[parentID]), 2*nodeRadius(geoms[nodeID].Kind))
	quant := quantizeDirToStep(dirOf(geoms[nodeID]), newDir, step)

	// New Dir for the moved node; re-propagate the whole graph from this geom set.
	// The mover adopts this Dir via its own "center" message (below) — no direct
	// cross-goroutine geom write here.
	newQuantDir := &[3]float64{quant.X, quant.Y, quant.Z}
	g := geoms[nodeID]
	g.Dir = newQuantDir
	geoms[nodeID] = g

	newCenters := computeSphereChainPositions(geoms, edges)
	if len(newCenters) == 0 {
		return nil, false
	}
	// Fan the fresh centers out through inboxes (one "center" message per node, routed
	// to that node's mover AND every incident edge's mover). Each owning goroutine
	// writes the center/Dir onto its own held geom and re-emits — preserving the
	// per-goroutine ownership model. The whole-graph SOLVE is central; the per-mover
	// APPLY stays decentralized, exactly like a Cell move's fan-out.
	for id, c := range newCenters {
		cc := c
		dir := geoms[id].Dir
		if ch, ok := md.dispatch[id]; ok {
			ch <- moveMsg{Kind: moveMsgKindCenter, NodeID: id, Center: &cc, Dir: dir}
		}
		for edgeID, em := range md.edgeMovers {
			if em.srcID == id || em.dstID == id {
				if ch, ok := md.dispatch[edgeID]; ok {
					ch <- moveMsg{Kind: moveMsgKindCenter, NodeID: id, Center: &cc, Dir: dir}
				}
			}
		}
	}
	return newQuantDir, true
}

// SphereResize sets the given node's sphere radius R and re-propagates every node's
// world center from the anchor (computeSphereChainPositions), fanning the fresh centers
// out to every node/edge mover (same decentralized APPLY as SphereMove). Changing a
// node's R changes the distance to ITS children, so the node's subtree contracts/expands
// toward it. r is clamped to a small minimum. Anchor-1 only: upstream nodes do not move
// (a full re-root would need per-edge re-anchoring — follow-up). Returns the clamped R
// and true on a real change; 0,false for an unknown node.
func (md *MoveDispatch) SphereResize(nodeID string, r float64) (float64, bool) {
	nm, ok := md.nodeMovers[nodeID]
	if !ok {
		return 0, false
	}
	const minR = 1.0
	if r < minR {
		r = minR
	}
	// Set R on the resized node's held geom (single owner mutates its own geom here,
	// matching how SphereMove sets Dir on the local geoms copy before re-propagating).
	rr := r
	nm.geom.R = &rr

	geoms := make(map[string]nodeGeom, len(md.nodeMovers))
	for id, m := range md.nodeMovers {
		geoms[id] = m.geom
	}
	edges := make([]sphereEdge, 0, len(md.edgeMovers))
	for _, em := range md.edgeMovers {
		edges = append(edges, sphereEdge{Source: em.srcID, Target: em.dstID})
	}
	newCenters := computeSphereChainPositions(geoms, edges)
	if len(newCenters) == 0 {
		return 0, false
	}
	for id, c := range newCenters {
		cc := c
		dir := geoms[id].Dir
		if ch, ok := md.dispatch[id]; ok {
			ch <- moveMsg{Kind: moveMsgKindCenter, NodeID: id, Center: &cc, Dir: dir}
		}
		for edgeID, em := range md.edgeMovers {
			if em.srcID == id || em.dstID == id {
				if ch, ok := md.dispatch[edgeID]; ok {
					ch <- moveMsg{Kind: moveMsgKindCenter, NodeID: id, Center: &cc, Dir: dir}
				}
			}
		}
	}
	return r, true
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

