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
//   - "" or "move": node-move — currently a no-op (sphere-chain layout owns positioning via "center" messages).
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
	// Center (Kind == "center"): the re-relaxed world center for NodeID under the
	// non-rooted layout. Each owning node/edge goroutine writes it onto its held geom
	// and re-emits its own geometry. SphereDrag relaxes the whole graph centrally and
	// fans the fresh centers out — the one centralized step (sphere_layout.go).
	Center *vec3
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
		// Non-rooted re-relaxation: adopt the centrally-computed world center, then
		// re-emit node-geometry.
		m.geom.Center = msg.Center
		m.geom.ReachR = msg.ReachR
		if m.tr != nil {
			emitNodeGeometry(m.tr, m.id, m.geom)
		}
		return
	}
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
		// Non-rooted re-relaxation: adopt the centrally-computed center on whichever
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
	// Plain "move" messages have no effect under sphere-chain layout;
	// position updates arrive as "center" messages instead.
	_ = msg
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
	// roots is the polar layout (container prism/origin + per-node outer polar
	// coordinate), built at load from the loaded world centers. Authoritative for
	// the polar move/lock logic; world positions recover via roots.world(id).
	roots rootSet
	// locks are polar relationships re-derived after a RootMove (lock.go).
	locks []chordLock
}

// setRoots installs the polar layout built at load (buildRoots).
func (md *MoveDispatch) setRoots(rs rootSet) { md.roots = rs }

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
// held node carries an explicit R.
// When false, applyEdit keeps the lattice (Cell) move path.
func (md *MoveDispatch) sphereChainActive() bool {
	for _, nm := range md.nodeMovers {
		if nm.geom.R != nil {
			return true
		}
	}
	return false
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
func (md *MoveDispatch) fanCenters(newCenters map[string]vec3, reach map[string]float64) {
	for id, c := range newCenters {
		cc := c
		rr := reach[id]
		if ch, ok := md.dispatch[id]; ok {
			ch <- moveMsg{Kind: moveMsgKindCenter, NodeID: id, Center: &cc, ReachR: rr}
		}
		for edgeID, em := range md.edgeMovers {
			if em.srcID == id || em.dstID == id {
				if ch, ok := md.dispatch[edgeID]; ok {
					ch <- moveMsg{Kind: moveMsgKindCenter, NodeID: id, Center: &cc, ReachR: rr}
				}
			}
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
	// Authority: update the dragged node's root. Center is the derived world value.
	md.roots.roots[nodeID] = rootFromCartesian(target, md.roots.origin)

	centers := md.heldCenters()
	edges := md.heldEdges()
	centers[nodeID] = target // soft membership: only the dragged node moves
	reach := reachRFromCenters(centers, edges)

	// Fan: the moved node (new center) plus each center it is a surface node of
	// (center unchanged, reach grew). reachRFromCenters already used the new
	// position, so the re-emitted ReachR reflects the grown sphere.
	emit := map[string]vec3{nodeID: target}
	for _, e := range edges {
		if e.Target == nodeID && e.Source != "" {
			if c, ok := centers[e.Source]; ok {
				emit[e.Source] = c
			}
		}
	}
	md.fanCenters(emit, reach)
	md.applyLocks(nodeID)
	return true
}

// reachRFromCenters computes each node's sphere REACH radius (max distance from a
// node's center to any node it outputs to) under the given centers and edge set.
// Mirrors loader.go buildFromSpec; used by SphereDrag so the fanned "center" message
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

