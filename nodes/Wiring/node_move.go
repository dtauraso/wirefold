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
	"sort"
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
	// moveMsgKindDragStart arms the dragged node X's OWN drag-anchor snapshot: X's
	// per-neighbor LocalPolar triples AT DRAG START, captured once on X's own goroutine
	// (nodeMover.handle), so every subsequent requantizeLocalPolars call during the same
	// drag reports current-minus-ANCHOR (the drag's running total) instead of
	// current-minus-previous-move-event (which is almost always 0 — RootMove runs on
	// every ~8ms pointer-move, far finer than one quantize step). Sent from gesture.go's
	// gestPending->gestDragging transition (the one place a drag begins), the same edge
	// that already emits tr.AbcDragReset() — see that call site's comment for why it
	// must not live in RootMove. Sent via the BLOCKING md.sendMove (not sendMoveLossy):
	// a dropped drag-start would silently leave X's anchor either unset (falling back to
	// the lazy-arm-on-first-commit path below, which is still correct but anchors one
	// commit later than the true drag start) or, worse, STALE from a prior drag if this
	// were the second+ drag on the same node — sendMoveLossy's "drop is safe, self-heals
	// next commit" reasoning does not apply here, so this rides the same
	// must-not-be-dropped guarantee as drag/center (see sendMoveLossy's doc comment).
	moveMsgKindDragStart = "dragStart"
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
	// and re-emits its own geometry. (RootMove is the decentralized node-to-node
	// message cascade entry; there is no central fan-out step.)
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
	// DeltaA/DeltaB/DeltaC (Kind == "neighborSetC"): the DRAGGED node's (SenderID's)
	// own quantized-triple change (newTriple - oldTriple, integer indices) for ITS edge
	// to this receiver, computed ONCE on SenderID's own goroutine in
	// requantizeLocalPolars. Pure observability payload — the receiver reports it on the
	// in-editor drag-log; it never applies it or recomputes its own position from it
	// (that stays exactly the receiver-computes reposition already in place). Zero if
	// SenderID had no prior stored triple to this receiver to subtract from.
	DeltaA, DeltaB, DeltaC int
	// Target (Kind == "drag"): the raw drag target world position for NodeID's
	// owner-goroutine commit. Every node is a free move, so this is committed as-is.
	Target vec3
	// testDone: see the type comment. Test-only; production leaves it nil.
	testDone chan struct{}
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
	// persist groups the six debounced disk persisters (move_persist.go), each nil until
	// armed by EnableViewpointPersist / EnableEditPersist after the startup seed. Grouped
	// the same way vp/ov/gest are, so a bare test-constructed MoveDispatch only has to
	// reason about one zero-value sub-struct instead of six loose nilable fields.
	persist persisters
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
	// sel groups the CURRENTLY-SELECTED (click-select) and CURRENTLY-HOVERED (pointer hover)
	// UI-only state (selection_state.go) — pure routing-directory-parked UI state, owned by
	// Go but not part of the dispatch/persist/camera concerns. Grouped the same way
	// vp/ov/gest are.
	sel selectionState
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
	// ctx is the process-lifetime context, captured in Start. sendMove (the bare
	// blocking directory send, used by external entry points like RootMove with
	// no owning mover goroutine to thread a ctx from) selects on ctx.Done() so a
	// send into a full inbox aborts on shutdown instead of leaking the calling
	// goroutine forever. nil in tests that build a bare MoveDispatch without
	// calling Start — sendMove treats a nil ctx as "no cancellation available"
	// and falls back to the plain blocking send (matches prior test behavior).
	ctx context.Context
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
		// Go 1.22+ loop semantics give each iteration its own id, so this closure safely
		// captures THIS iteration's id (no shared-variable capture bug).
		nm.layoutHolderFn = func() *LayoutHolder { return md.layoutHolders[id] }
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
	md.ctx = ctx
	for _, nm := range md.nodeMovers {
		go nm.run(ctx)
		// Dedicated sender goroutine for this node's outbound queue (see the outbox
		// type doc comment / cascade-deadlock-fix.md): pops nm's queued messages in
		// FIFO order and performs the actual blocking delivery, so nm's own
		// inbox-drain goroutine (nm.run above) never blocks on a send and therefore
		// always keeps draining its inbox. deliverMove itself selects on ctx.Done()
		// (closure-captured here, at the goroutine's own spawn site) so a send into
		// a full/abandoned inbox aborts on shutdown instead of leaking this sender
		// goroutine forever.
		go nm.outbox.run(ctx, func(id string, msg moveMsg) { md.deliverMove(ctx, id, msg) })
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
	ch, ok := md.dispatch[id]
	if !ok {
		return
	}
	// Blocking send with a ctx-cancel escape hatch: this is the bare directory
	// send used by external entry points (RootMove) that have no owning mover
	// goroutine to thread a ctx from. Without the
	// ctx.Done() arm, a send into a torn-down/full inbox on shutdown parks this
	// goroutine forever (the target's inbox-drain goroutine has already
	// returned on the same ctx cancel, so nothing will ever drain it). md.ctx is
	// nil only in tests that build a bare MoveDispatch without Start — a nil
	// Context's Done() channel would panic, so guard it and fall back to a
	// plain blocking send there (matches prior test behavior; no shutdown path
	// exists in that setting anyway).
	if md.ctx == nil {
		ch <- msg
		return
	}
	select {
	case ch <- msg:
	case <-md.ctx.Done():
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
//
// ctx is threaded in from the owning outbox.run's own goroutine (Start closure-captures
// it at spawn) rather than stored on MoveDispatch: on ctx cancel every mover's inbox-drain
// goroutine (nm.run) returns and stops draining, so a sender goroutine parked mid-send on
// `ch <- msg` would otherwise block forever with nothing left to ever read it. The
// select's ctx.Done() arm is ONLY a cancellation escape — the send itself stays BLOCKING
// while ctx is live (this must not become a lossy drop like sendMoveLossy; every
// deliverMove message carries real committed geometry that a drop would leave stale).
func (md *MoveDispatch) deliverMove(ctx context.Context, id string, msg moveMsg) {
	ch, ok := md.dispatch[id]
	if !ok {
		return
	}
	select {
	case ch <- msg:
	case <-ctx.Done():
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

// NodeKind returns the kind string for the given node id, or "" if unknown.
// Used by applyEdit to resolve the node's kind when snapping a port-anchor
// world-space direction to the nearest ring-anchor index. Called from the
// gesture/stdin-reader goroutine (gesture.go:164, :653), which is NOT the
// nodeMover's own goroutine — this is the ONE genuine cross-goroutine read of
// nm.geom, so it takes nm.geomMu.
//
// It copies the WHOLE struct under the lock rather than plucking .Kind out, and
// that is deliberate. A bare `return nm.geom.Kind` cannot race today — Kind is
// write-once (loader.go, at load) and Go's race detector is byte-range precise,
// so a .Kind read never overlaps applyCenter's writes to ScenePolar/HasPos/ReachR.
// That made the lock unfalsifiable: the guard could be deleted and NOTHING —
// no test, not even -race — would notice, which is how a mutex comment drifts
// into being wrong (this one already was, twice).
//
// Reading the full struct, exactly as emitGeometry does, makes the guard
// load-bearing: remove the lock and -race reports a real conflict against
// applyCenter. TestNodeKindConcurrentWithApplyCenterUnderRace is that check.
// The cost is copying one nodeGeom (two slice headers, no element copy).
func (md *MoveDispatch) NodeKind(nodeID string) string {
	if nm, ok := md.nodeMovers[nodeID]; ok {
		nm.geomMu.Lock()
		geom := nm.geom
		nm.geomMu.Unlock()
		return geom.Kind
	}
	return ""
}

// Overlay-visibility API (MoveDispatch delegators), the overlayState methods, the
// overlayToggles table, defaultOverlayState, and the stdinGuideVisPayload mapper are all
// GENERATED into overlay_gen.go from OVERLAY_FLAG_NAMES (tools/gen-node-defs).
