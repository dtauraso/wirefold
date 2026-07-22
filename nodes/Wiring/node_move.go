// node_move.go — decentralized node-move handling.
//
// A node-move is NOT handled by a central coordinator. Instead the loader wires each
// mover's dedicated inbound channels: there is no shared many-to-one inbox — every pair of movers that talk (a node and its
// incident edge, or two nodes joined by an edge) gets its OWN directed channel, plus every
// mover gets one dedicated "external" channel for the stdin/gesture goroutine's rare direct
// entries. The stdin reader's whole job for a move is to write each entry onto the ONE
// channel that entry addresses. No recompute, no topology logic lives in the reader.
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
	"sync"

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
	// to the dragged node's OWN dedicated extIn channel instead of the stdin reader
	// committing on its behalf. The receiver commits its OWN new position via the owner-goroutine commit
	// path (commitNodeMoveLocal, which applies its own new center SYNCHRONOUSLY via
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
	// must not live in RootMove. Sent via the BLOCKING md.sendMove: a dropped drag-start
	// would silently leave X's anchor either unset (falling back to the
	// lazy-arm-on-first-commit path below, which is still correct but anchors one commit
	// later than the true drag start) or, worse, STALE from a prior drag if this were the
	// second+ drag on the same node — so this must never be dropped, same as drag/center.
	moveMsgKindDragStart = "dragStart"
)

// posReport is one mover's freshly-committed world center, reported over the
// movers→gesture report channel (MoveDispatch.posReportCh) the same way
// portTblC/edgeTblC/nodeTblC hand row tables from the Trace-drain goroutine to the
// gesture goroutine: an external-observer channel, not a node↔node inbox. Sent
// non-blockingly by applyCenter (the sole writer of a node's center), drained by
// drainPositions into md.positions.
type posReport struct {
	ID     string
	Center vec3
}

// posReportBufSize sizes the buffered movers→gesture report channel. A single drag
// commits on ONE mover's own clock-paced cycle at a time (applyCenter fires once per
// commitNodeMoveLocal call, i.e. once per RootMove pointer-move tick on the dragged
// node), so the channel only ever needs to hold the handful of reports produced
// between two drainPositions calls (one per stdin dispatch iteration) — generously
// sized well past that so a drag is never dropped before the gesture goroutine drains.
const posReportBufSize = 256

// moveMsg is one entry routed to one of a mover's own dedicated channels (there is no
// shared inbox). kind selects the
// payload:
//   - "" or "move": node-move — currently a no-op (polar-layout positions all nodes via "center" messages).
//
// Every PRODUCTION send is fire-and-forget: the sender drops the message onto the
// destination's own channel and returns. No production path observes the receiver finishing — a node does its own
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
	// PartnerCenter (Kind == "center", Center == nil): a pure aimed-port re-emit
	// request ALSO carries the SENDER's (SenderID's) just-committed fresh center, so
	// the receiver can update its own nm.neighborCenters[SenderID] cache before
	// re-emitting — the aimed-port marker's direction reads that cache at emit time
	// (see nodeMover.neighborCenters / buildPartnerCenterFn's wiring). Kept distinct
	// from Center (which, if non-nil here, would be misread as "this is YOUR OWN new
	// center" by the moveMsgKindCenter handler and wrongly move the receiver itself).
	PartnerCenter *vec3
	// testDone: see the type comment. Test-only; production leaves it nil.
	testDone chan struct{}
}

// MoveDispatch is the pure registry built at load that owns every mover and wires their
// dedicated channels together — there is no shared dispatch map anymore. The node-mover
// directory itself is LOAD-TIME-LOCAL: newMoveDispatch builds it, wires each mover's
// resolveDest closure to capture it, hands it back to the loader for the remaining
// construction-time lookups (quantOffset seed, partnerCenter), and then drops it — no
// nodeMovers FIELD survives. What the runtime still needs from that directory is served by
// narrow frozen tables instead: md.extRoute (editor→node sends), md.kinds (NodeKind), and
// md.loadCenters (content-fit). md.edgeMovers is still a field (edges are looked up by every
// per-commit fan). It also retains the per-edge source Outs so out-of-package test/verifier callers can
// read an edge's loaded geometry (EdgeOut) without going through a central coordinator.
type MoveDispatch struct {
	edgeMovers map[string]*edgeMover
	// nodeMoverList is a plain ENUMERATION (not an id-keyed lookup directory) of every
	// nodeMover built at construction, kept solely so Start can launch one goroutine per
	// node. Nothing looks a mover up BY ID through this field — every runtime id-keyed
	// need is served by md.extRoute/md.kinds/md.loadCenters instead (see those fields'
	// doc comments and newMoveDispatch's doc comment for why the id-keyed directory
	// itself is load-time-local and dropped after construction).
	nodeMoverList []*nodeMover
	// kinds is the immutable node-id → kind directory, built once at construction from
	// the load-time geoms and never mutated afterward (a node's kind is write-once
	// identity — port_geometry.go nodeIdentity). It is the whole of what NodeKind needs;
	// keeping it as its own frozen table (rather than reaching into a mover's geom) makes
	// the cross-goroutine kind read lock-free BY CONSTRUCTION — there is no writer to race
	// — and lets the mover directory itself become load-time-local (Job 1).
	kinds map[string]string
	// loadCenters is the immutable node-id → LOAD-TIME world center directory, built once
	// at construction from the same geoms md.positions is seeded from. Distinct from
	// md.positions (which the gesture goroutine mutates as nodes report drags): loadCenters
	// is the frozen load-time snapshot the content-fit scene sphere is derived from
	// (loadTimeCenters → contentFitSceneSphere, scene_sphere_persist.go). Previously this
	// was re-read live off every mover's geom; freezing it here removes loadTimeCenters'
	// last dependence on the mover directory.
	loadCenters map[string]vec3
	// extRoute is the EDITOR→NETWORK send directory: node id → that node's own
	// dedicated external-entry channel (nodeMover.extIn). It is the ONLY thing the
	// external caller (the gesture/stdin goroutine — RootMove's drag, gesture.go's
	// dragStart and ring-anchor sends) needs from a node: a channel to drop an
	// addressed entry onto. Distinct from the full mover directory (the loader-local
	// nodeMovers map) on purpose — the editor addresses a node to SEND to it, it never needs the
	// mover struct itself; keeping the send path on this narrow table lets the mover
	// directory become load-time-local (it no longer has to survive as the editor's
	// lookup). Built once at construction alongside each nodeMover; a read-only
	// directory afterward, safe from any goroutine.
	extRoute map[string]chan moveMsg
	// positions is the gesture goroutine's OWN accumulated map of every node's last-
	// reported world center — written ONLY by drainPositions (from posReportCh) and by
	// the one-time SeedPositions call before Start launches any mover goroutine, read
	// ONLY on the gesture goroutine (heldCenters, centerOfNode). This is the external-
	// observer pattern portTbl/edgeTbl/nodeTbl already use, generalized to node
	// centers: movers report, the gesture goroutine drains into its own map, nothing
	// reads across that boundary the other way.
	positions map[string]vec3
	// posReportCh is the buffered movers→gesture report channel every nodeMover's
	// applyCenter sends its fresh {id, center} on (posReportBufSize). Created once here
	// at construction and handed to each nodeMover as reportCh; nil in bare test
	// construction that skips newMoveDispatch, in which case applyCenter's send is a
	// guarded no-op (nil channel + select-default) and drainPositions is a no-op (nil
	// channel is never selected).
	posReportCh chan posReport
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
	// portTbl/edgeTbl/nodeTbl are this (stdin/gesture) goroutine's OWN plain-field copies of
	// the buffer's row-lookup tables, filled by drainRowTables from portTblC/edgeTblC/
	// nodeTblC (below) — never read or written by any other goroutine, so no lock/atomic is
	// needed (CLAUDE.md model: ownership over sharing). portFromHit/edgeFromHit/nodeFromHit
	// (gesture.go) read these directly. nil until the first table arrives (startup: a hit
	// can arrive before Buffer ever sends one) — the lookups below treat nil exactly like an
	// empty table (out-of-range), the same not-found sentinel the old atomic.Load-then-nil-
	// check gave.
	portTbl []T.PortRow
	edgeTbl []string
	nodeTbl []string
	// portTblC/edgeTblC/nodeTblC are the receive ends of the depth-1, replace-latest
	// channels the Trace-drain goroutine's Buffer.SnapshotState sends rebuilt row tables on
	// (SetPortTableChan/SetEdgeTableChan/SetNodeTableChan in main.go wire the SAME channel
	// object to both the Buffer send side and this receive side). nil on the old path and in
	// unit tests, in which case drainRowTables is a no-op (a receive on a nil channel is
	// never selected) and portTbl/edgeTbl/nodeTbl stay whatever a test set them to directly.
	portTblC chan []T.PortRow
	edgeTblC chan []string
	nodeTblC chan []string
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
	// ctx is the process-lifetime context, captured in Start. sendMove (the bare
	// blocking directory send, used by external entry points like RootMove with
	// no owning mover goroutine to thread a ctx from) selects on ctx.Done() so a
	// send into a full inbox aborts on shutdown instead of leaking the calling
	// goroutine forever. nil in tests that build a bare MoveDispatch without
	// calling Start — sendMove treats a nil ctx as "no cancellation available"
	// and falls back to the plain blocking send (matches prior test behavior).
	ctx context.Context
}

// SetNodeTableChan wires the receive end of the depth-1, replace-latest node-row-table
// channel (the send end is Buffer.SnapshotState.SetNodeTableChan on the SAME channel
// object — main.go creates it and wires both ends). Called once at startup after
// LoadTopology. drainRowTables (called at the top of each stdin dispatch iteration) is what
// actually moves a pending value into md.nodeTbl; this just installs the channel.
func (md *MoveDispatch) SetNodeTableChan(ch chan []string) { md.nodeTblC = ch }

// SetEdgeTableChan is the edge-row analogue of SetNodeTableChan.
func (md *MoveDispatch) SetEdgeTableChan(ch chan []string) { md.edgeTblC = ch }

// SetPortTableChan is the port-row analogue of SetNodeTableChan.
func (md *MoveDispatch) SetPortTableChan(ch chan []T.PortRow) { md.portTblC = ch }

// drainRowTables non-blockingly pulls the latest pending value (if any) off each of
// portTblC/edgeTblC/nodeTblC into md.portTbl/md.edgeTbl/md.nodeTbl. Called at the top of
// every stdin dispatch iteration (RunStdinReader, stdin_reader.go) — BEFORE any hit
// resolution in that iteration — so a hit resolves against the freshest table delivered so
// far, matching the freshness the old atomic.Load gave. A nil channel (unwired: tests, or a
// build that never calls the Set*TableChan setters) makes every case here permanently
// unselectable, so this is a safe no-op in that case; md.portTbl/edgeTbl/nodeTbl then stay
// whatever a test set them to directly (or nil, at startup, before Buffer's first send).
func (md *MoveDispatch) drainRowTables() {
	select {
	case tbl := <-md.portTblC:
		md.portTbl = tbl
	default:
	}
	select {
	case tbl := <-md.edgeTblC:
		md.edgeTbl = tbl
	default:
	}
	select {
	case tbl := <-md.nodeTblC:
		md.nodeTbl = tbl
	default:
	}
}

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
	// SrcPort/DstPort are the edge's endpoint port names (source OUT-port, dest
	// IN-port) — carried alongside the seed segment so main.go's seed tr.Geometry
	// call can thread port identity onto the seed row the same way the node's own
	// live EmitGeometry does (Buffer/snapshot.go derives the Edge block's endpoint
	// coordinates from the Port block by this identity).
	SrcPort, DstPort       string
	SX, SY, SZ, EX, EY, EZ float64
}

// NodeSeeds returns every node's load-time seed geometry in SPEC ORDER (see
// MoveDispatch.nodeSeeds). Call after LoadTopology returns, before launching any node
// goroutine, and stream each entry via tr.NodeGeometry (main.go).
func (md *MoveDispatch) NodeSeeds() []NodeGeomSeed { return md.nodeSeeds }

// EdgeSeeds returns every edge's load-time seed topology (with real endpoint geometry) in
// SPEC ORDER. Call alongside NodeSeeds; stream each entry via tr.Geometry (main.go).
func (md *MoveDispatch) EdgeSeeds() []EdgeGeomSeed { return md.edgeSeeds }

// newMoveDispatch builds the registry from per-node geometry and per-edge endpoints.
// It creates one nodeMover per node and one edgeMover per edge, registering each edge
// under its id in md.edgeMovers and each node in the loader-local nodeMovers map (returned,
// not a field), and wires the dedicated
// directed channels between adjacent movers. Outs and dest wires are bound later by Bind once node
// construction has populated them. nodeOrder/edgeOrder are the
// SPEC order (deterministic directory-sorted order, not map iteration order) used to
// build md.nodeSeeds/edgeSeeds for buffer row seeding.
//
// speedSinks, when non-nil, is the loader's build-wide accumulator
// (buildCtx.speedSinks): each nodeMover AND each edgeMover created below gets its own
// fresh buffered-1 speed channel (per-goroutine-clock.md "Delivery" — every
// clock-owning goroutine must not be left behind), and that channel's SEND end is
// appended here.
// nil in test call sites that construct a MoveDispatch directly with no
// loader — those edgeMovers then simply have no speed channel to poll.
// It returns the built *MoveDispatch AND the node-mover directory it built. The directory
// is LOAD-TIME-LOCAL by design (Job 1): every runtime lookup is served by the frozen
// md.extRoute/md.kinds/md.loadCenters tables and the resolveDest/partnerCenter closures
// (which capture the directory), so the only remaining callers of the returned map are the
// loader's own construction-time seeds (quantOffset, partnerCenter) — after which it is
// dropped. Tests that build a MoveDispatch this way and need to reach a specific mover take
// it from this return value; MoveDispatch itself keeps no nodeMovers field.
func newMoveDispatch(geoms map[string]nodeGeom, edgeEndpoints map[string]EdgeEndpoints, tr *T.Trace, nodeOrder, edgeOrder []string, clk Clock, speedSinks *[]chan float64) (*MoveDispatch, map[string]*nodeMover) {
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
		edgeMovers:    map[string]*edgeMover{},
		edgeOut:       map[string]*Out{},
		tr:            tr,
		ov:            defaultOverlayState(),
		layoutHolders: map[string]*LayoutHolder{},
		positions:     map[string]vec3{},
		posReportCh:   make(chan posReport, posReportBufSize),
		extRoute:      map[string]chan moveMsg{},
		kinds:         map[string]string{},
		loadCenters:   map[string]vec3{},
	}
	// nodeMovers is LOAD-TIME-LOCAL — see this function's doc comment. Every mover's
	// resolveDest closure and the partnerCenter wiring below capture it; the runtime never
	// reads it (md.extRoute/kinds/loadCenters serve those needs), and it is returned to the
	// loader for its remaining construction-time seeds, then dropped.
	nodeMovers := map[string]*nodeMover{}
	// Static partner-center lookup for the seed pass: every node's center is already known
	// off the load-time geoms map (no goroutine/atomic-snap needed), so this is the SAME
	// buildPartnerCenterFn the dynamic movers use below, just closed over geoms directly
	// instead of the dynamic movers' neighborCenters caches. Kept per-node (not shared) to match
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
			SrcPort: ep.SourceHandle, DstPort: ep.TargetHandle,
			SX: sx, SY: sy, SZ: sz, EX: ex, EY: ey, EZ: ez,
		})
	}
	for id, g := range geoms {
		nm := newNodeMover(id, g, tr, clk)
		nm.reportCh = md.posReportCh
		if speedSinks != nil {
			nodeSpeedCh := make(chan float64, 1)
			nm.speedCh = nodeSpeedCh
			*speedSinks = append(*speedSinks, nodeSpeedCh)
		}
		// resolveDest resolves the ONE dedicated directed channel FROM this node
		// (selfID, captured below) TO destID: another node's own neighborIn[selfID]
		// slot, or an incident edge's srcIn/dstIn depending on which endpoint this
		// node is. There is no shared dispatch map to look up — the loader-local nodeMovers
		// map (captured by this closure) and md.edgeMovers are the read-only directories,
		// safe to read from any goroutine once construction finishes.
		selfID := id
		nm.resolveDest = func(destID string) (chan moveMsg, bool) {
			if em, ok := md.edgeMovers[destID]; ok {
				switch selfID {
				case em.srcID:
					return em.srcIn, true
				case em.dstID:
					return em.dstIn, true
				}
				return nil, false
			}
			if other, ok := nodeMovers[destID]; ok {
				if ch, ok := other.neighborIn[selfID]; ok {
					return ch, true
				}
			}
			return nil, false
		}
		nm.sendMove = md.enqueueFor(nm)
		// commitLocal/neighborSetC capture THIS node's own mover so the owner-goroutine
		// commit path (commitNodeMoveLocal / neighborSetCRequantize) never has to look the
		// mover back up by id — there is no nodeMovers field to look it up in. handle passes
		// m.id purely for the (ignored) signature; the mover is the closure's own nm.
		ownMover := nm
		nm.commitLocal = func(_ string, newPos vec3) { md.commitNodeMoveLocal(ownMover, newPos) }
		nm.neighborSetC = md.neighborSetCRequantize
		// Go 1.22+ loop semantics give each iteration its own id, so this closure safely
		// captures THIS iteration's id (no shared-variable capture bug).
		nm.layoutHolderFn = func() *LayoutHolder { return md.layoutHolders[id] }
		nodeMovers[id] = nm
		md.extRoute[id] = nm.extIn
		md.kinds[id] = g.Kind
		md.nodeMoverList = append(md.nodeMoverList, nm)
	}
	for edgeID, ep := range edgeEndpoints {
		em := newEdgeMover(ep, edgeID, geoms[ep.Source], geoms[ep.Target], tr, clk)
		if speedSinks != nil {
			edgeSpeedCh := make(chan float64, 1)
			em.speedCh = edgeSpeedCh
			*speedSinks = append(*speedSinks, edgeSpeedCh)
		}
		md.edgeMovers[edgeID] = em
		// This edge's two nodes each get a dedicated channel TO this edge (already
		// created above, srcIn/dstIn) — and each other's own dedicated channel for
		// node-to-node traffic (neighborIn, the plain-neighbor/partner-reemit fan):
		// two directed channels per ordered pair, never a shared inbox.
		if srcNM, ok := nodeMovers[ep.Source]; ok {
			if dstNM, ok := nodeMovers[ep.Target]; ok {
				if _, exists := dstNM.neighborIn[ep.Source]; !exists {
					dstNM.neighborIn[ep.Source] = make(chan moveMsg, 8)
				}
				if _, exists := srcNM.neighborIn[ep.Target]; !exists {
					srcNM.neighborIn[ep.Target] = make(chan moveMsg, 8)
				}
				// Seed each side's neighborCenters cache from the load-time geoms —
				// safe here (main goroutine, before Start launches any mover
				// goroutine) — so a neighbor's aimed-port direction / requantize
				// offset has a valid center to read before either side ever
				// commits a move (mirrors the old atomic snap's construction-time
				// seed). Kept current afterward by moveMsgKindNeighborSetC and the
				// aimed-port pure-reemit's PartnerCenter field, both handled only
				// on the OWNING node's own goroutine (see nodeMover.handle).
				dstNM.neighborCenters[ep.Source] = nodeWorldPos(geoms[ep.Source])
				srcNM.neighborCenters[ep.Target] = nodeWorldPos(geoms[ep.Target])
			}
		}
	}
	// Wire each nodeMover's aimed-port lookup: for (port,isInput) on nodeID, find its one
	// edge (edgeEndpoints) and read the partner's center off THIS node's OWN
	// neighborCenters cache — written only by this node's own goroutine (handle), read
	// only by this node's own goroutine (emitGeometry/partnerCenter) — no cross-goroutine
	// map access, unlike the atomic snap this replaced.
	for id, nm := range nodeMovers {
		ownNM := nm
		nm.partnerCenter = buildPartnerCenterFn(id, edgeEndpoints, func(otherID string) vec3 {
			return ownNM.neighborCenters[otherID]
		})
	}
	// Give every nodeMover the ids of its OWN incident edges, so a lock-driven move can
	// notify its edges via sendMove (resolveDest's per-pair channel lookup) — no cached
	// channel slice.
	for id, nm := range nodeMovers {
		for edgeID, em := range md.edgeMovers {
			if em.srcID == id || em.dstID == id {
				nm.edgeIDs = append(nm.edgeIDs, edgeID)
			}
		}
	}
	// Seed md.positions from these same load-time geoms right here at construction —
	// the SeedPositions/loadTimeCenters public path exists for callers that build a
	// MoveDispatch some other way (or want to re-seed later), but every real
	// newMoveDispatch construction (production and every LoadTopology-based test) gets
	// this for free immediately, matching how the old atomic snap was seeded at
	// construction.
	for id, g := range geoms {
		c := nodeWorldPos(g)
		md.positions[id] = c
		// loadCenters is the frozen load-time snapshot (never mutated by drags, unlike
		// md.positions) that the content-fit scene sphere is derived from — see the field
		// doc comment and loadTimeCenters.
		md.loadCenters[id] = c
	}
	return md, nodeMovers
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

// Start launches every mover's goroutine — ONE goroutine per node and ONE per edge, no
// dedicated sender/watcher goroutines (an earlier shared-outbox-plus-sender-goroutine
// design was removed: each mover's own run loop drains its own inbox AND retries its own
// pending sends, non-blockingly, every cycle).
//
// Returns a *sync.WaitGroup covering every launched goroutine, so a caller that wants a
// complete shutdown (main.go: "wait for everything, then close" — see
// the wait-for-everything-then-close change) can wg.Wait() on it after cancelling
// ctx. Both nm.run and em.run select on ctx.Done() at the top of their loop (their only
// blocking call is SleepCycle, which also selects on ctx), so cancel-to-return is one
// clock tick, worst case. Callers that don't care about shutdown completeness (most
// existing tests) can ignore the return value — Start(ctx) alone still compiles and
// still launches every goroutine exactly as before.
func (md *MoveDispatch) Start(ctx context.Context) *sync.WaitGroup {
	md.ctx = ctx
	wg := new(sync.WaitGroup)
	for _, nm := range md.nodeMoverList {
		nm := nm
		wg.Add(1)
		go func() {
			defer wg.Done()
			nm.run(ctx)
		}()
	}
	for _, em := range md.edgeMovers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			em.run(ctx)
		}()
	}
	return wg
}

// EdgeOut returns the source *Out bound to the given edge label, or nil if unknown.
// Read-only accessor for out-of-package verifiers (the headless cascade reads an
// edge's per-edge in-flight time from the loaded geometry).
func (md *MoveDispatch) EdgeOut(edgeID string) *Out {
	return md.edgeOut[edgeID]
}

// centerOfNode returns the gesture goroutine's last-reported world center for a node
// id, from md.positions (see MoveDispatch.positions doc comment). GESTURE-GOROUTINE
// ONLY: every live caller (gesture.go's port-move-grab and node-drag-grab hit
// handling) runs on the stdin/gesture dispatch loop, the same goroutine drainPositions
// fills md.positions from.
func (md *MoveDispatch) centerOfNode(id string) (vec3, bool) {
	c, ok := md.positions[id]
	return c, ok
}

// drainPositions non-blockingly pulls every pending posReport off md.posReportCh into
// md.positions — the movers→gesture analogue of drainRowTables, called at the same
// point in RunStdinReader's dispatch loop. Drains the WHOLE backlog each call (not just
// one) so a burst of reports between two dispatch iterations is fully absorbed, matching
// nodeMover.run's own "drain to empty" drain shape. A nil posReportCh (bare test
// construction that skips newMoveDispatch) makes the receive permanently unselectable,
// so this is a safe no-op there.
func (md *MoveDispatch) drainPositions() {
	for {
		select {
		case r := <-md.posReportCh:
			if md.positions == nil {
				md.positions = map[string]vec3{}
			}
			md.positions[r.ID] = r.Center
		default:
			return
		}
	}
}

// loadTimeCenters returns the frozen load-time snapshot md.loadCenters (built once at
// newMoveDispatch construction, keyed by node id → world center at load time — never
// mutated by a later drag). Used to install the content-fit scene sphere and to seed
// md.positions (SeedPositions) before the gesture dispatch loop's first iteration, when
// md.positions/posReportCh have nothing in
// them yet (movers only ever report a center AFTER a real center commit).
func (md *MoveDispatch) loadTimeCenters() map[string]vec3 {
	return md.loadCenters
}

// SeedPositions seeds md.positions from load-time geometry. Call once on the main
// goroutine, after newMoveDispatch/LoadTopology and BEFORE Start launches any mover
// goroutine and before RunStdinReader's dispatch loop begins — so the very first
// gesture/camera interaction (which reads md.positions via heldCenters/centerOfNode)
// has data, rather than an empty map that only fills once a mover commits its first real
// move.
func (md *MoveDispatch) SeedPositions() {
	if md.positions == nil {
		md.positions = map[string]vec3{}
	}
	for id, c := range md.loadTimeCenters() {
		md.positions[id] = c
	}
}

// sendMove routes one moveMsg to a node's dedicated external-entry channel (extIn), if
// the id is a known node. This is the EXTERNAL-caller path — RootMove (drag) and
// gesture.go's dragStart send — not a mover-to-mover send (those go through a mover's
// own nm.pending/flushPending onto its OWN dedicated channel, never through this
// function). md.extRoute is a read-only directory once construction finishes, safe to
// read from any goroutine.
func (md *MoveDispatch) sendMove(id string, msg moveMsg) {
	ch, ok := md.extRoute[id]
	if !ok {
		return
	}
	// Blocking send with a ctx-cancel escape hatch: this is the bare external-entry
	// send used by callers (RootMove, gesture.go) that have no owning mover goroutine
	// to thread a ctx from. Without the ctx.Done() arm, a send into a torn-down/full
	// extIn on shutdown parks this goroutine forever (the target's own run loop has
	// already returned on the same ctx cancel, so nothing will ever drain it). md.ctx
	// is nil only in tests that build a bare MoveDispatch without Start — a nil
	// Context's Done() channel would panic, so guard it and fall back to a plain
	// blocking send there (matches prior test behavior; no shutdown path exists in
	// that setting anyway).
	if md.ctx == nil {
		ch <- msg
		return
	}
	select {
	case ch <- msg:
	case <-md.ctx.Done():
	}
}

// enqueueFor returns nm's own non-blocking send function: it appends the message to
// nm's own pending retry queue and attempts an immediate flush — never blocking the
// calling handler goroutine. Bound once per node at construction (nm.sendMove =
// md.enqueueFor(nm)) so every send a nodeMover's own handle performs — including the
// ones fanEdgesAndPartners/requantizeLocalPolars make on that node's behalf — goes
// through nm's own retry queue, never a raw blocking channel write and never a second
// mover's queue (there is no shared outbox to route through anymore).
func (md *MoveDispatch) enqueueFor(nm *nodeMover) func(id string, msg moveMsg) {
	return func(id string, msg moveMsg) {
		nm.pending = append(nm.pending, pendingSend{destID: id, msg: msg})
		nm.flushPending()
	}
}

// NOTE (neighborSetC drop history): moveMsgKindNeighborSetC (requantizeLocalPolars'
// per-neighbor fan, quantized_move.go) used to route through a non-blocking
// sendMoveLossy — "receiver is mid-cascade and will self-requantize on its own next
// commit, so a full-inbox drop is safe" was the reasoning, and blocking was avoided to
// dodge a mutual-adjacency deadlock (two nodes each trying to set-c the other while
// both inboxes are full and neither is draining). Measuring it under the SAME
// concurrent mutually-adjacent drag flood TestMutuallyAdjacentDragFloodNoDeadlock
// drives (TestNeighborSetCDropReachability) showed sendMoveLossy dropping ~98% of
// NeighborSetC sends (9417/9600 in one run) — the drop path was not a rare backstop,
// it was silently discarding almost every message. NeighborSetC is now routed through
// the SENDING node's own retry queue (nm.sendMove in requantizeLocalPolars,
// see nodeMover.pending), the same decoupling every other per-commit fan in this file
// already uses (fanEdgesAndPartners) — it gets the same deadlock-avoidance property (the
// send never blocks the handler goroutine) without ever dropping: an item that can't be
// delivered right now stays with the sender and is retried on the sender's own next
// loop cycle instead of being handed to a separate sender goroutine or dropped.
// TestMutuallyAdjacentDragFloodNoDeadlock still passes with this change, so the deadlock
// risk sendMoveLossy was guarding against does not require a lossy send.

// NodeKind returns the kind string for the given node id, or "" if unknown. Served from
// the immutable md.kinds table built once at newMoveDispatch construction; it is never
// mutated afterward, so this cross-goroutine read is lock-free by construction — there
// is no writer to race against.
func (md *MoveDispatch) NodeKind(nodeID string) string {
	return md.kinds[nodeID]
}

// Overlay-visibility API (MoveDispatch delegators), the overlayState methods, the
// overlayToggles table, defaultOverlayState, and the stdinGuideVisPayload mapper are all
// GENERATED into overlay_gen.go from OVERLAY_FLAG_NAMES (tools/gen-node-defs).
