// Buffer/snapshot.go — full-state column-block snapshot builder (Phase 2).
//
// SnapshotState accumulates world render state from trace events and produces
// framed binary snapshots on the position-emit cadence (~16 ms).
//
// Output channel: binary frames are written to a dedicated file descriptor
// (default fd 3, overridable via WIREFOLD_BUF_OUT_FD env var; set to "0" to
// disable). The JSON trace on stdout (fd 1) is completely untouched.
//
// Frame format: [len:u32-LE][snapshot bytes]
//
// Snapshot layout (little-endian, packed):
//
//	Header   40 bytes: [tick][beadCount][nodeCount][edgeCount][portCount][labelBytesCount][eventCount][portNameBytesCount][edgeLabelBytesCount][layoutLinkCount] (u32 each)
//	Bead     beadCount × BufBeadStride bytes
//	Node     nodeCount × BufNodeStride bytes   (persistent geom + transient event flags + label off/len)
//	Interior nodeCount × BufInteriorSlotsPerNode × BufInteriorStride bytes
//	Edge     edgeCount × BufEdgeStride bytes   (+ edge-label off/len)
//	LayoutLink layoutLinkCount × BufLayoutLinkStride bytes (the LAYOUT double-link overlay pairs,
//	           from LocalPolars — NOT the Edge block; see bufLayoutLayoutLink in layout.go)
//	Port     portCount × BufPortStride bytes   (flattened over nodes in node-row order; + port-name off/len)
//	Camera   BufCameraStride bytes             (always 1 row)
//	Overlay  BufOverlayStride bytes            (always 1 row)
//	Scene    BufSceneStride bytes              (always 1 row; persisted scene-sphere center+radius)
//	Label    labelBytesCount bytes             (node labels' UTF-8 bytes, node-row order)
//	Event    eventCount × BufEventStride bytes (per-tick causal trace events; .probe log only)
//	PortName portNameBytesCount bytes          (port names' UTF-8 bytes, flattened port-row order)
//	EdgeLabel edgeLabelBytesCount bytes        (edge labels' UTF-8 bytes, edge-row order)
//
// At the rollout flip (a later phase), this becomes the sole framed stdout once
// the JSON trace is removed. For now it runs alongside JSON on a side channel.
//
// All SnapshotState methods must be called from a single goroutine (the Trace
// drain goroutine). No internal synchronisation.

package Buffer

import (
	"encoding/binary"
	"fmt"
	"io"
	"sync/atomic"

	T "github.com/dtauraso/wirefold/Trace"
)

// PortRowEntry is one row of the port-row resolution table: the (node, port) identity
// that a numeric buffer PORT-ROW index resolves to. Go writes the buffer Port block in a
// fixed flattened order (node-row order × each node's Ports order); this table is built in
// that SAME order, so port row i ↔ table entry i. It is the authoritative row→(node,port)
// map the gesture FSM uses to turn a raw port-row hit back into a topology edit — the
// numeric buffer carries no port-name strings (no sidecar).
type PortRowEntry struct {
	Node    string
	Port    string
	IsInput bool
}

// SnapshotState accumulates full-world render state from trace events.
type SnapshotState struct {
	// Node rows: stable-ordered by first KindNodeGeometry event.
	nodeIDs   []string
	nodeIndex map[string]int
	nodes     []nodeSnapState

	// Edge rows: stable-ordered by first KindGeometry event.
	edgeLabels []string
	edgeIndex  map[string]int
	edges      []edgeSnapState

	// LayoutLink rows: stable-ordered by first KindLayoutLink event. Distinct from edges —
	// sourced from the LAYOUT model (LocalPolars), not the bead-edge graph. Keyed by
	// "src\x00dst" so a re-emit of the same pair (e.g. a respawn re-running load) is idempotent
	// rather than appending a duplicate row.
	layoutLinkIndex map[string]int
	layoutLinks     []layoutLinkSnapState

	// Live in-flight beads, keyed by (sourceNode, sourcePort, gen).
	beads map[beadSnapKey]beadSnapState

	// srcToDest maps (sourceNode, sourcePort) → destination node ID.
	// Populated from KindSend Target fields so arrive events can route EvArrive
	// to the correct destination node.
	srcToDest map[srcPortKey]string

	// Camera, overlay, and scene-sphere singletons (always one row each in the snapshot).
	camera  cameraSnapState
	overlay overlaySnapState
	scene   sceneSnapState

	// tick is the monotonic snapshot sequence counter.
	tick uint32

	// out receives framed binary snapshots. Nil = silent discard.
	out io.Writer

	// portTable publishes the current flattened port-row table (same order as the Port
	// block) as an immutable slice. Rebuilt on node-geometry changes (the only place ports
	// change) on the Trace-drain goroutine and read via LookupPortRow from the stdin/gesture
	// goroutine — the atomic pointer hands off an immutable snapshot with no lock.
	portTable atomic.Pointer[[]PortRowEntry]

	// edgeTable publishes the current edge-row table (the edge labels in the SAME stable
	// row order as the Edge block) as an immutable slice. Rebuilt whenever a new edge is
	// registered (onEdgeGeometry) on the Trace-drain goroutine and read via LookupEdgeRow
	// from the gesture goroutine — the atomic pointer hands off an immutable snapshot with
	// no lock. This is the edge analogue of portTable: a numeric edge-row hit resolves to
	// its edge label so the gesture FSM can mark the Go-owned edge selection.
	edgeTable atomic.Pointer[[]string]

	// nodeTable publishes the current node-row table (the node ids in the SAME stable row
	// order as the Node block) as an immutable slice. Rebuilt whenever a new node registers
	// (onNodeGeometry) on the Trace-drain goroutine and read via LookupNodeRow from the
	// gesture goroutine — the atomic pointer hands off an immutable snapshot with no lock.
	// This is the node analogue of portTable/edgeTable: a numeric node-row hit resolves to
	// its node id so the gesture FSM can drag/select the Go-owned node.
	nodeTable atomic.Pointer[[]string]

	// pendingEvents accumulates the per-tick causal trace events since the last snapshot
	// emit (the same accumulate-then-flush lifecycle as the transient node event flags).
	// buildSnapshot resolves each to numeric rows + string-section slices and writes the
	// EVENT block; clearTransients drops them. Consumed ONLY by the ext-host buffer-decoded
	// .probe logger — the render path ignores the EVENT block.
	pendingEvents []eventRec

	// kindID maps a trace-event kind string to its index in T.TraceEventKinds (the shared
	// Go/TS vocabulary), which is the EVENT block's Kind column. Built once in NewSnapshotState.
	kindID map[string]uint8

	// overlayFlagFields maps a bare-visibility-toggle trace Kind to the *uint8 field on
	// s.overlay it sets. Collapses the nine identical
	// "s.overlay.X = boolU8(ev.Visible); s.emitSnapshot()" Update cases into one lookup+set.
	// Built once in NewSnapshotState (pointers into s.overlay, valid for the state's
	// lifetime since overlay is a fixed-offset struct field, not reallocated).
	overlayFlagFields map[string]*uint8

	// tickSource, when non-nil, is the network's one human-speed clock (Wiring.Clock.Tick),
	// injected via SetTickSource. It coalesces the high-volume KindPosition stream (one event
	// PER BEAD per tick, so beads_in_flight events per tick) down to at most one emit per tick,
	// matching "the tick IS the animation clock" (clock.go) instead of publishing once per bead.
	// Buffer must not import nodes/Wiring (one-way dependency), so this is injected as a plain
	// func, not a Wiring.Clock. Nil (the default, e.g. bare NewSnapshotState in tests) preserves
	// the original per-event emit behavior exactly.
	tickSource func() int64

	// lastPosEmitTick is the tick at which a KindPosition event last actually triggered an
	// emitSnapshot; -1 = none yet (so the first position event on a fresh tick always emits).
	lastPosEmitTick int64

	// breadcrumb, if set, receives the DEBUG BREADCRUMB channel (wired to tr.Breadcrumb in
	// main.go). SnapshotState runs on the single Trace-drain goroutine, so calling it from a
	// build method is goroutine-safe. Nil in headless tests (a no-op).
	breadcrumb func(label, node, port, value string)
	// lastDroppedLayoutLinks is the count of layout links dropped by resolvableLayoutLinks
	// last build, so the drop is breadcrumbed only when it CHANGES — never per-snapshot (the
	// build path runs hundreds of times/sec; a per-build breadcrumb would flood the channel).
	lastDroppedLayoutLinks int

	// positionDirty is true when bead state has changed since the last emitSnapshot call for
	// any reason. Set on every KindPosition update; cleared inside emitSnapshot (which publishes
	// the current bead state regardless of what triggered it). FinalFlush checks this so a
	// coalesced-away final tick's bead positions are never dropped at shutdown.
	positionDirty bool
}

// SetTickSource injects the network's tick function so the KindPosition stream coalesces to
// at most one emit per tick. Call once at startup (main.go, after both the clock and
// SnapshotState exist); leave unset (nil) to keep tests' original per-event emit semantics.
func (s *SnapshotState) SetTickSource(f func() int64) {
	s.tickSource = f
}

// eventRec is one buffered causal event, holding string identities that buildSnapshot
// resolves to numeric rows / string-section slices when it writes the EVENT block.
type eventRec struct {
	kind         string
	node, port   string
	portIsInput  bool
	target       string
	targetHandle string
	edge         string
	slot         int
	value        int
	bead         uint64
	arc, lat     float64
	x, y, z, f   float64
	flag         bool // visible (overlay toggles)
}

type beadSnapKey struct {
	node string
	port string
	gen  uint64
}

type srcPortKey struct {
	node string
	port string
}

// nodeSnapState holds persistent geometry + status + transient event flags for
// one node. Transient fields (evRecv…evDone) are cleared after each snapshot.
type nodeSnapState struct {
	cx, cy, cz      float64
	radius, sphereR float64
	// vr*/fr* are the two great-circle ring-plane normals (vertical vr, flat fr) from the
	// node-geometry event; SphereRing orients its two tori by these.
	vrx, vry, vrz float64
	frx, fry, frz float64
	// label is the node's human label (from the node-geometry event's Label; data.label
	// else the id). Streamed as UTF-8 bytes in the snapshot's trailing label section, keyed
	// by this node's LabelOff/LabelLen columns — no sidecar.
	label    string
	evRecv   uint8
	evFire   uint8
	evSend   uint8
	evArrive uint8
	evDone   uint8
	// selected is PERSISTENT (not a transient event flag): 1 marks this node as the
	// current click-selected node. Set/cleared by KindSelect; NOT reset in clearTransients.
	selected uint8
	// hovered is PERSISTENT (not a transient event flag): 1 marks this node as the one under
	// the pointer. Set/cleared by KindHover; NOT reset in clearTransients.
	hovered uint8
	// latchedSel is PERSISTENT: 1 marks this node as the LAST node that was click-selected.
	// Unlike selected, it does NOT clear when the node is deselected (clicking empty space) —
	// only selecting a DIFFERENT node moves it. Set alongside selected in setSelected.
	latchedSel uint8
	// kindID is the node's kind as its index into NODE_DEFS_ARRAY (from NodeKindID).
	// Set once on first KindNodeGeometry; subsequent re-emits don't change kind.
	kindID uint8
	// interior holds this node's 2x2 held/interior-bead grid (slot = row*2 + col).
	// PERSISTENT — a slot keeps its state until the next KindNodeBead updates it
	// (present=false explicitly clears a popped slot). Not touched by clearTransients.
	interior [BufInteriorSlotsPerNode]interiorSlotState
	// ports holds this node's port geometry (input + output), from the node-geometry
	// event's Ports. PERSISTENT — re-emitted on every node-move (only the dirs change;
	// the port set/order is stable), so buildSnapshot flattens the current ports across
	// all nodes in node-row order into the Port block. The numeric buffer carries no port
	// strings; a port hit is resolved by row index via the Go-side port-row table.
	ports []portSnapState
}

// portSnapState holds one port's unit surface direction (node center → port) and
// whether it is an input port. Populated from a node-geometry event's Ports.
type portSnapState struct {
	name       string
	dx, dy, dz float64
	px, py, pz float64
	isInput    bool
	// hovered is PERSISTENT: 1 marks this port as the one under the pointer. Set/cleared by
	// KindHover; NOT reset in clearTransients. Preserved across node-geometry re-emits below.
	hovered uint8
}

// interiorSlotState holds one interior grid slot's present/value + Go-owned
// NODE-LOCAL offset (relative to the node center).
type interiorSlotState struct {
	present    uint8
	value      int32
	ox, oy, oz float64
}

// edgeSnapState holds persistent segment endpoints for one edge, plus the edge's
// source and destination node ids (edge-graph topology used by the on-surface
// selection highlight). srcNode/dstNode are resolved to node-row indices at
// buildSnapshot time (a node may register after its edges do).
type edgeSnapState struct {
	sx, sy, sz float64
	ex, ey, ez float64
	srcNode    string
	dstNode    string
	// selected is PERSISTENT (not a transient event flag): 1 marks this edge as the
	// current click-selected edge. Set/cleared by KindSelect (Edge field); exclusive with
	// node selection. Not reset in clearTransients.
	selected uint8
}

// layoutLinkSnapState holds one LAYOUT-link pair's endpoint node ids. Resolved to node-row
// indices at buildSnapshot time, same as edgeSnapState's srcNode/dstNode.
type layoutLinkSnapState struct {
	srcNode string
	dstNode string
}

// beadSnapState holds current position + metadata for one in-flight bead.
type beadSnapState struct {
	x, y, z float64
	value   int
}

// cameraSnapState mirrors the camera block (single row).
type cameraSnapState struct {
	px, py, pz       float64
	r                float64
	posTheta, posPhi float64
	upTheta, upPhi   float64
}

// sceneSnapState mirrors the scene-sphere block (single row): the persisted world anchor
// every node's scene polar is measured about. Established ONCE at load and never moved
// (see KindSceneSphere); zero-value (radius 0) until that one-time startup event arrives,
// mirroring the sphereR "0 = not yet populated" convention used elsewhere in this file.
type sceneSnapState struct {
	cx, cy, cz float64
	radius     float64
}

// overlaySnapState mirrors the overlay block (single row).
type overlaySnapState struct {
	sceneTori      uint8
	scenePoles     uint8
	nodePoles      uint8
	selSpherePoles uint8
	handholds      uint8
	labelsGlobal   uint8
	overlaysVis    uint8
	doubleLinks    uint8
}

// NewSnapshotState creates an empty SnapshotState that writes framed snapshots
// to out. Pass nil for out to build snapshots without emitting them (useful in
// tests that only inspect state).
func NewSnapshotState(out io.Writer) *SnapshotState {
	s := &SnapshotState{
		nodeIndex:       map[string]int{},
		edgeIndex:       map[string]int{},
		layoutLinkIndex: map[string]int{},
		beads:           map[beadSnapKey]beadSnapState{},
		srcToDest:       map[srcPortKey]string{},
		out:             out,
		kindID:          buildKindIDMap(),
		lastPosEmitTick: -1,
	}
	s.overlayFlagFields = map[string]*uint8{
		T.KindSceneTori:      &s.overlay.sceneTori,
		T.KindScenePoles:     &s.overlay.scenePoles,
		T.KindNodePoles:      &s.overlay.nodePoles,
		T.KindSelSpherePoles: &s.overlay.selSpherePoles,
		T.KindHandholds:      &s.overlay.handholds,
		T.KindLabelsGlobal:   &s.overlay.labelsGlobal,
		T.KindOverlaysVis:    &s.overlay.overlaysVis,
		T.KindDoubleLinks:    &s.overlay.doubleLinks,
	}
	return s
}

// buildKindIDMap indexes T.TraceEventKinds so the EVENT block Kind column matches the
// TS TRACE_EVENT_KINDS array (both generated from Trace.go's Kind* constants).
func buildKindIDMap() map[string]uint8 {
	m := make(map[string]uint8, len(T.TraceEventKinds))
	for i, k := range T.TraceEventKinds {
		m[k] = uint8(i)
	}
	return m
}

// Update processes one trace event, updating the snapshot state.
// Must be called from the Trace drain goroutine on every event.
// On KindPosition events it also triggers a snapshot emit.
func (s *SnapshotState) Update(ev T.Event) {
	// Record the causal event for the buffer-decoded .probe log BEFORE mutating state, so the
	// EVENT block flushed by the next emitSnapshot mirrors exactly the events seen this window.
	s.recordEvent(ev)
	switch ev.Kind {
	case T.KindNodeGeometry:
		s.onNodeGeometry(ev)
		s.emitSnapshot() // state-change point: emit on geometry updates
	case T.KindGeometry:
		s.onEdgeGeometry(ev)
		s.emitSnapshot() // state-change point: emit on edge geometry updates
	case T.KindLayoutLink:
		s.onLayoutLink(ev)
		s.emitSnapshot() // state-change point: emit on layout-link registration
	case T.KindCamera:
		s.camera = cameraSnapState{
			px: ev.PX, py: ev.PY, pz: ev.PZ,
			r:        ev.R,
			posTheta: ev.PosTheta, posPhi: ev.PosPhi,
			upTheta: ev.UpTheta, upPhi: ev.UpPhi,
		}
		s.emitSnapshot() // state-change point: emit on camera changes
	case T.KindSceneSphere:
		// The scene sphere is established ONCE at load and never moves (MODEL.md); Go emits
		// this a single time at startup, so a plain assign (not a merge) is correct.
		s.scene = sceneSnapState{cx: ev.PX, cy: ev.PY, cz: ev.PZ, radius: ev.R}
		s.emitSnapshot()

	case T.KindSceneTori, T.KindScenePoles, T.KindNodePoles,
		T.KindSelSpherePoles, T.KindHandholds, T.KindLabelsGlobal,
		T.KindOverlaysVis, T.KindDoubleLinks:
		if field, ok := s.overlayFlagFields[ev.Kind]; ok {
			*field = boolU8(ev.Visible)
		}
		s.emitSnapshot()

	case T.KindPosition:
		// Update the live bead state. Every bead steps once per tick (paced_wire.go), so with
		// N beads in flight this event fires N times per tick — but the tick, not the event, is
		// the animation clock (clock.go), so coalesce to at most one emit per tick: emit only
		// when tickSource reports a tick we have not yet emitted for this stream. positionDirty
		// tracks any update an emit skip left unpublished, so FinalFlush cannot drop it.
		k := beadSnapKey{ev.Node, ev.Port, ev.Bead}
		s.beads[k] = beadSnapState{
			x: ev.X, y: ev.Y, z: ev.Z,
			value: ev.Value,
		}
		s.positionDirty = true
		if s.tickSource == nil {
			s.emitSnapshot()
		} else if t := s.tickSource(); t != s.lastPosEmitTick {
			s.lastPosEmitTick = t
			s.emitSnapshot()
		}

	case T.KindArrive:
		// Bead completed traversal: remove it from live beads.
		delete(s.beads, beadSnapKey{ev.Node, ev.Port, ev.Bead})
		// Set EvArrive on the DESTINATION node. The arrive event carries the SOURCE
		// (node+port); look up the destination via the srcToDest map built from sends.
		if dest, ok := s.srcToDest[srcPortKey{ev.Node, ev.Port}]; ok {
			s.setNodeEvent(dest, BufEventArriveID)
		}

	case T.KindPulseCancelled:
		delete(s.beads, beadSnapKey{ev.Node, ev.Port, ev.Bead})

	case T.KindRecv:
		s.setNodeEvent(ev.Node, BufEventRecvID)

	case T.KindFire:
		s.setNodeEvent(ev.Node, BufEventFireID)

	case T.KindSend:
		s.setNodeEvent(ev.Node, BufEventSendID)
		// Record the src→dest mapping when Target is present (wire sends).
		if ev.Target != "" {
			s.srcToDest[srcPortKey{ev.Node, ev.Port}] = ev.Target
		}

	case T.KindDone:
		s.setNodeEvent(ev.Node, BufEventDoneID)

	case T.KindNodeBead:
		// One interior grid slot's authoritative state (node's 2x2 held/interior
		// grid). Persistent per-node slot state; present=false clears a popped slot.
		// X/Y/Z are the Go-owned NODE-LOCAL offset (renderer adds the node center).
		s.setInteriorSlot(ev.Node, ev.Row, ev.Col, ev.Present, int32(ev.Value), ev.X, ev.Y, ev.Z)
		s.emitSnapshot() // state-change point: emit on interior-bead updates

	case T.KindSelect:
		// Go-owned selection: a select event marks EITHER one node (ev.Node) OR one edge
		// (ev.Edge) — never both (the gesture FSM enforces the exclusivity). ev.Edge!=""
		// selects that edge and clears any node selection; otherwise ev.Node selects that
		// node (or clears everything when empty) and clears any edge selection. Persistent —
		// survives across snapshots until the next select. Emit so the change is reflected
		// in the buffer immediately.
		if ev.Edge != "" {
			s.setSelectedEdge(ev.Edge)
		} else {
			s.setSelected(ev.Node)
		}
		s.emitSnapshot()

	case T.KindHover:
		// Go-owned hover: a hover event marks EITHER one port (ev.Port != "", on node
		// ev.Node, ev.Value=1 for an input port) OR one node (ev.Node), never both. It clears
		// all other node/port hover flags. ev.Node=="" && ev.Port=="" clears all hover.
		// Persistent until the next hover; emit so the change reflects in the buffer.
		s.setHovered(ev.Node, ev.Port, ev.Value == 1)
		s.emitSnapshot()
	}
}

// PortCount returns the total number of port rows across all nodes (for tests).
func (s *SnapshotState) PortCount() int {
	n := 0
	for i := range s.nodes {
		n += len(s.nodes[i].ports)
	}
	return n
}

// BuildSnapshot exposes the snapshot builder for tests.
func (s *SnapshotState) BuildSnapshot() []byte { return s.buildSnapshot() }

// rebuildPortTable rebuilds and atomically publishes the port-row table in the SAME
// flattened order buildSnapshot writes the Port block: for each node in stable node-row
// order, that node's ports in node-geometry Ports order. Called from the Trace-drain
// goroutine whenever a node-geometry event changes the port set. The published slice is
// immutable — LookupPortRow reads it lock-free from another goroutine.
func (s *SnapshotState) rebuildPortTable() {
	tbl := make([]PortRowEntry, 0, s.PortCount())
	for i := range s.nodes {
		node := s.nodeIDs[i]
		for _, p := range s.nodes[i].ports {
			tbl = append(tbl, PortRowEntry{Node: node, Port: p.name, IsInput: p.isInput})
		}
	}
	s.portTable.Store(&tbl)
}

// LookupPortRow resolves a numeric buffer PORT-ROW index to its (node, port, isInput)
// identity via the published port-row table. ok=false for an out-of-range row or before
// any port has been registered. Safe to call from a goroutine other than the Trace drain
// (reads an immutable atomically-published slice). This is the row→(node,port) resolution
// the gesture FSM uses for wiring/handhold — the numeric buffer carries no port strings.
func (s *SnapshotState) LookupPortRow(row int) (node, port string, isInput, ok bool) {
	tbl := s.portTable.Load()
	if tbl == nil || row < 0 || row >= len(*tbl) {
		return "", "", false, false
	}
	e := (*tbl)[row]
	return e.Node, e.Port, e.IsInput, true
}

// rebuildNodeTable rebuilds and atomically publishes the node-row table — the node ids in
// the SAME stable row order buildSnapshot writes the Node block (node id insertion order).
// Called from the Trace-drain goroutine whenever a new node registers. The published slice
// is immutable — LookupNodeRow reads it lock-free from the gesture goroutine.
func (s *SnapshotState) rebuildNodeTable() {
	tbl := make([]string, len(s.nodeIDs))
	copy(tbl, s.nodeIDs)
	s.nodeTable.Store(&tbl)
}

// LookupNodeRow resolves a numeric buffer NODE-ROW index to its node id via the published
// node-row table. ok=false for an out-of-range row or before any node registers. This is the
// node analogue of LookupPortRow/LookupEdgeRow: a numeric node-row hit (the node
// InstancedMesh instanceId == its buffer node row) resolves back to the node id here in Go,
// so the numeric buffer carries no node id strings and the webview forwards only the row.
func (s *SnapshotState) LookupNodeRow(row int) (nodeID string, ok bool) {
	tbl := s.nodeTable.Load()
	if tbl == nil || row < 0 || row >= len(*tbl) {
		return "", false
	}
	return (*tbl)[row], true
}

// --- internal helpers --------------------------------------------------------

func (s *SnapshotState) onNodeGeometry(ev T.Event) {
	id := ev.Node
	if _, exists := s.nodeIndex[id]; !exists {
		s.nodeIndex[id] = len(s.nodeIDs)
		s.nodeIDs = append(s.nodeIDs, id)
		s.nodes = append(s.nodes, nodeSnapState{kindID: NodeKindID(ev.NodeKind)})
		// A new node row exists: republish the node-row table (same stable row order as the
		// Node block) so a numeric node-row hit resolves to its node id.
		s.rebuildNodeTable()
	}
	idx := s.nodeIndex[id]
	n := &s.nodes[idx]
	n.cx, n.cy, n.cz = ev.NX, ev.NY, ev.NZ
	n.radius = ev.Radius
	n.sphereR = ev.SphereR
	n.vrx, n.vry, n.vrz = ev.VRX, ev.VRY, ev.VRZ
	n.frx, n.fry, n.frz = ev.FRX, ev.FRY, ev.FRZ
	// Label: the node's human label (stable per run; re-set on each re-emit is harmless).
	// Streamed as bytes in the snapshot label section, keyed by this row's LabelOff/LabelLen.
	n.label = ev.Label
	// Port geometry: replace this node's ports with the event's current port set/dirs
	// (re-emit on move updates the dirs; the port set/order is stable). Kept in the
	// event's Ports order so the buffer Port block and the Go-side port-row table stay
	// in the same flattened row order.
	// Preserve any per-port hover flag across this re-emit: a node-move re-emits geometry
	// (only the dirs change; the port set/order is stable), and hover must not flicker off
	// mid-hover. Key by (name, isInput) since name alone can repeat across in/out.
	prevHover := make(map[[2]any]uint8, len(n.ports))
	for _, p := range n.ports {
		prevHover[[2]any{p.name, p.isInput}] = p.hovered
	}
	n.ports = n.ports[:0]
	for _, p := range ev.Ports {
		n.ports = append(n.ports, portSnapState{
			name: p.Name, dx: p.DX, dy: p.DY, dz: p.DZ,
			px: p.PX, py: p.PY, pz: p.PZ, isInput: p.IsInput,
			hovered: prevHover[[2]any{p.Name, p.IsInput}],
		})
	}
	// Republish the port-row table: ports (and node order) just changed. Built in the SAME
	// flattened order buildSnapshot writes the Port block, so port row i ↔ entry i.
	s.rebuildPortTable()
}

func (s *SnapshotState) onEdgeGeometry(ev T.Event) {
	label := ev.Edge
	if _, exists := s.edgeIndex[label]; !exists {
		s.edgeIndex[label] = len(s.edgeLabels)
		s.edgeLabels = append(s.edgeLabels, label)
		s.edges = append(s.edges, edgeSnapState{})
		// A new edge row exists: republish the edge-row table (same stable row order as the
		// Edge block) so a numeric edge-row hit resolves to its label.
		s.rebuildEdgeTable()
	}
	idx := s.edgeIndex[label]
	e := &s.edges[idx]
	e.sx, e.sy, e.sz = ev.SX, ev.SY, ev.SZ
	e.ex, e.ey, e.ez = ev.EX, ev.EY, ev.EZ
	// Node (source) and Target (dest) carry the edge's endpoint node ids for the
	// on-surface adjacency; preserve any previously-set ids if a later emit omits them.
	if ev.Node != "" {
		e.srcNode = ev.Node
	}
	if ev.Target != "" {
		e.dstNode = ev.Target
	}
}

// onLayoutLink registers one LAYOUT-link pair (Node=one endpoint, Target=the other), sourced
// from LocalPolars (nodes/Wiring/loader.go emitLayoutLinks) — NOT the bead-edge graph. Idempotent
// on the unordered pair key so a re-emit does not append a duplicate row.
func (s *SnapshotState) onLayoutLink(ev T.Event) {
	key := ev.Node + "\x00" + ev.Target
	if _, exists := s.layoutLinkIndex[key]; exists {
		return
	}
	s.layoutLinkIndex[key] = len(s.layoutLinks)
	s.layoutLinks = append(s.layoutLinks, layoutLinkSnapState{srcNode: ev.Node, dstNode: ev.Target})
}

// setNodeEvent sets one transient event flag on a node by BufEvent* id.
func (s *SnapshotState) setNodeEvent(nodeID string, eventID int) {
	idx, ok := s.nodeIndex[nodeID]
	if !ok {
		return
	}
	n := &s.nodes[idx]
	switch eventID {
	case BufEventRecvID:
		n.evRecv = 1
	case BufEventFireID:
		n.evFire = 1
	case BufEventSendID:
		n.evSend = 1
	case BufEventArriveID:
		n.evArrive = 1
	case BufEventDoneID:
		n.evDone = 1
	}
}

// setInteriorSlot records one interior grid slot's state on a node. slot = row*2 + col;
// out-of-range (row,col) or unknown nodes are ignored. Persistent — survives across
// snapshots until the next node-bead updates the slot.
func (s *SnapshotState) setInteriorSlot(nodeID string, row, col int, present bool, value int32, ox, oy, oz float64) {
	idx, ok := s.nodeIndex[nodeID]
	if !ok {
		return
	}
	slot := row*2 + col
	if slot < 0 || slot >= BufInteriorSlotsPerNode {
		return
	}
	s.nodes[idx].interior[slot] = interiorSlotState{
		present: boolU8(present), value: value, ox: ox, oy: oy, oz: oz,
	}
}

// setSelected marks nodeID as the selected node and clears the flag on every other
// node. nodeID=="" clears all selection. Persistent state — not touched by
// clearTransients.
func (s *SnapshotState) setSelected(nodeID string) {
	sel := -1
	if nodeID != "" {
		if idx, ok := s.nodeIndex[nodeID]; ok {
			sel = idx
		}
	}
	for i := range s.nodes {
		if i == sel {
			s.nodes[i].selected = 1
			// latchedSel moves to the newly-selected node; a deselect (sel == -1) leaves
			// every node's latchedSel untouched here (the loop below never sets latchedSel
			// on i == -1), so the PREVIOUSLY latched node stays latched through deselect.
			s.nodes[i].latchedSel = 1
		} else {
			s.nodes[i].selected = 0
			if sel >= 0 {
				s.nodes[i].latchedSel = 0
			}
		}
	}
	// Node selection is exclusive with edge selection: selecting/clearing a node clears
	// any selected edge.
	s.clearSelectedEdges()
}

// setSelectedEdge marks the edge with the given label selected and clears the flag on
// every other edge; it also clears any node selection (selection is single + exclusive).
// An unknown label clears all edge selection. Persistent — not touched by clearTransients.
func (s *SnapshotState) setSelectedEdge(label string) {
	sel := -1
	if idx, ok := s.edgeIndex[label]; ok {
		sel = idx
	}
	for i := range s.edges {
		if i == sel {
			s.edges[i].selected = 1
		} else {
			s.edges[i].selected = 0
		}
	}
	// Exclusive with node selection.
	for i := range s.nodes {
		s.nodes[i].selected = 0
	}
}

// setHovered marks the hovered entity and clears hover on every other node and port. A
// non-empty port hovers that (node, port, isInput); otherwise a non-empty node hovers that
// node; both empty clears all hover. Persistent — not touched by clearTransients.
func (s *SnapshotState) setHovered(nodeID, port string, isInput bool) {
	// Clear all hover first.
	for i := range s.nodes {
		s.nodes[i].hovered = 0
		for j := range s.nodes[i].ports {
			s.nodes[i].ports[j].hovered = 0
		}
	}
	idx, ok := s.nodeIndex[nodeID]
	if !ok {
		return // unknown/empty node → nothing to set (already cleared)
	}
	if port != "" {
		for j := range s.nodes[idx].ports {
			p := &s.nodes[idx].ports[j]
			if p.name == port && p.isInput == isInput {
				p.hovered = 1
				return
			}
		}
		return // port not found → leave node unhovered (a port hover is not a node hover)
	}
	s.nodes[idx].hovered = 1
}

// clearSelectedEdges clears the selected flag on every edge.
func (s *SnapshotState) clearSelectedEdges() {
	for i := range s.edges {
		s.edges[i].selected = 0
	}
}

// rebuildEdgeTable rebuilds and atomically publishes the edge-row table — the edge labels
// in the SAME stable row order buildSnapshot writes the Edge block (edge label insertion
// order). Called from the Trace-drain goroutine whenever a new edge registers. The
// published slice is immutable — LookupEdgeRow reads it lock-free from another goroutine.
func (s *SnapshotState) rebuildEdgeTable() {
	tbl := make([]string, len(s.edgeLabels))
	copy(tbl, s.edgeLabels)
	s.edgeTable.Store(&tbl)
}

// LookupEdgeRow resolves a numeric buffer EDGE-ROW index to its edge label via the
// published edge-row table. ok=false for an out-of-range row or before any edge registers.
// Safe to call from a goroutine other than the Trace drain (reads an immutable atomically-
// published slice). This is the row→edge resolution the gesture FSM uses to mark the
// Go-owned edge selection — the numeric buffer carries no edge label strings.
func (s *SnapshotState) LookupEdgeRow(row int) (label string, ok bool) {
	tbl := s.edgeTable.Load()
	if tbl == nil || row < 0 || row >= len(*tbl) {
		return "", false
	}
	return (*tbl)[row], true
}

// nodeRowIndex returns the buffer node-row index for a node id, or -1 when the id is
// empty or not yet registered (edges can register before their endpoint nodes do).
func (s *SnapshotState) nodeRowIndex(nodeID string) int {
	if nodeID == "" {
		return -1
	}
	if idx, ok := s.nodeIndex[nodeID]; ok {
		return idx
	}
	return -1
}

// clearTransients resets all transient node event flags to 0 after snapshot emit.
func (s *SnapshotState) clearTransients() {
	for i := range s.nodes {
		n := &s.nodes[i]
		n.evRecv = 0
		n.evFire = 0
		n.evSend = 0
		n.evArrive = 0
		n.evDone = 0
	}
	// Drop the per-tick causal events now that they have been packed into the emitted
	// snapshot's EVENT block (same accumulate-then-flush lifecycle as the flags above).
	s.pendingEvents = s.pendingEvents[:0]
}

// emitSnapshot builds one snapshot, writes a framed frame to s.out, then
// clears transient event flags. Ignores write errors (nothing reads this fd
// yet — on-but-harmless until the rollout flip phase).
func (s *SnapshotState) emitSnapshot() {
	// Defer any event whose identity references a node/edge not yet registered (e.g. an
	// Input node's startup send fires before its target's geometry is emitted). Such an event
	// would resolve to row -1 and drop its target/handle from the log; carry it forward to the
	// next emit instead, when the referenced entity is registered. Ordering is irrelevant to
	// the log (a multiset of events), and by end-of-run every node/edge is registered.
	ready := s.pendingEvents[:0:0]
	deferred := make([]eventRec, 0)
	for _, e := range s.pendingEvents {
		if s.eventReady(e) {
			ready = append(ready, e)
		} else {
			deferred = append(deferred, e)
		}
	}
	s.pendingEvents = ready

	snap := s.buildSnapshot()
	if s.out != nil {
		var hdr [4]byte
		binary.LittleEndian.PutUint32(hdr[:], uint32(len(snap)))
		// Write errors are intentionally ignored: this is the fire-and-forget Go→TS render
		// stream (CLAUDE.md — no ack, no delivery signal), emitted every tick. Logging on
		// failure would be a per-tick firehose (see log-flood lesson), and there is no caller
		// that could act on the error. A dead peer (broken pipe) is a lifecycle event: the
		// host tears the Go process down, so there is nothing to recover here.
		_, _ = s.out.Write(hdr[:])
		_, _ = s.out.Write(snap)
	}
	s.clearTransients()
	// Restore the deferred events (clearTransients truncated pendingEvents to empty).
	s.pendingEvents = append(s.pendingEvents, deferred...)
	// This emit just published the current bead state (buildSnapshot always packs s.beads),
	// regardless of what triggered it — clear the coalesce-tracking flag.
	s.positionDirty = false
}

// eventReady reports whether every node/edge identity an event references is registered, so
// buildSnapshot can resolve it to a real row (not the -1 sentinel that would drop it from the
// decoded log).
func (s *SnapshotState) eventReady(e eventRec) bool {
	if e.node != "" && s.nodeRowIndex(e.node) < 0 {
		return false
	}
	if e.target != "" && s.nodeRowIndex(e.target) < 0 {
		return false
	}
	if e.edge != "" {
		if _, ok := s.edgeIndex[e.edge]; !ok {
			return false
		}
	}
	return true
}

// snapshotBuild holds all the per-build derived data (counts, string sections,
// total size) that buildSnapshot's block-writer helpers read from. Computed once per build by
// newSnapshotBuild; the block writers never recompute it.
type snapshotBuild struct {
	beadCount, nodeCount, edgeCount uint32
	layoutLinkCount                 uint32
	// renderableLayoutLinks is the layout-link pairs whose BOTH endpoints resolve to a live
	// node row this build (resolvableLayoutLinks). layoutLinkCount == len(this), and
	// writeLayoutLinkBlock iterates THIS — never s.layoutLinks — so an unresolvable endpoint
	// is filtered before emit and a -1 SrcNodeRow/DstNodeRow can never reach the buffer.
	renderableLayoutLinks    []layoutLinkSnapState
	interiorCount, portCount int
	eventCount               int

	labelBytes      []byte
	labelOffs       []uint32
	labelLens       []uint32
	labelBytesCount int

	portNameBytes      []byte
	portNameOffs       []uint32
	portNameLens       []uint32
	portNameBytesCount int

	edgeLabelBytes      []byte
	edgeLabelOffs       []uint32
	edgeLabelLens       []uint32
	edgeLabelBytesCount int

	size int
}

// buildLabelSection concatenates every node's label UTF-8 bytes in node-row order; each
// node's LabelOff/LabelLen columns slice into the returned bytes.
func (s *SnapshotState) buildLabelSection() ([]byte, []uint32, []uint32) {
	nodeCount := len(s.nodes)
	labelBytes := make([]byte, 0, nodeCount*8)
	labelOffs := make([]uint32, nodeCount)
	labelLens := make([]uint32, nodeCount)
	for i := range s.nodes {
		labelOffs[i] = uint32(len(labelBytes))
		lb := []byte(s.nodes[i].label)
		labelLens[i] = uint32(len(lb))
		labelBytes = append(labelBytes, lb...)
	}
	return labelBytes, labelOffs, labelLens
}

// buildPortNameSection concatenates every port's name UTF-8 bytes in the SAME flattened
// port-row order as the Port block; each port's PortNameOff/PortNameLen slice into the
// returned bytes. Carried for the .probe buffer-decoded log.
func (s *SnapshotState) buildPortNameSection(portCount int) ([]byte, []uint32, []uint32) {
	portNameBytes := make([]byte, 0, portCount*8)
	portNameOffs := make([]uint32, 0, portCount)
	portNameLens := make([]uint32, 0, portCount)
	for i := range s.nodes {
		for _, p := range s.nodes[i].ports {
			portNameOffs = append(portNameOffs, uint32(len(portNameBytes)))
			pb := []byte(p.name)
			portNameLens = append(portNameLens, uint32(len(pb)))
			portNameBytes = append(portNameBytes, pb...)
		}
	}
	return portNameBytes, portNameOffs, portNameLens
}

// buildEdgeLabelSection concatenates every edge's label UTF-8 bytes in stable edge-row order;
// each edge's EdgeLabelOff/EdgeLabelLen slice into the returned bytes. Carried for the .probe
// buffer-decoded log (geometry/select-edge).
func (s *SnapshotState) buildEdgeLabelSection() ([]byte, []uint32, []uint32) {
	edgeCount := len(s.edges)
	edgeLabelBytes := make([]byte, 0, edgeCount*8)
	edgeLabelOffs := make([]uint32, edgeCount)
	edgeLabelLens := make([]uint32, edgeCount)
	for i := range s.edges {
		edgeLabelOffs[i] = uint32(len(edgeLabelBytes))
		eb := []byte(s.edgeLabels[i])
		edgeLabelLens[i] = uint32(len(eb))
		edgeLabelBytes = append(edgeLabelBytes, eb...)
	}
	return edgeLabelBytes, edgeLabelOffs, edgeLabelLens
}

// newSnapshotBuild computes all counts and the trailing string sections once per
// buildSnapshot call, plus the total buffer size. The block-writer helpers read from
// the returned struct only — none of them recompute this data.
func (s *SnapshotState) newSnapshotBuild() *snapshotBuild {
	renderableLayoutLinks := s.resolvableLayoutLinks()
	b := &snapshotBuild{
		beadCount:             uint32(len(s.beads)),
		nodeCount:             uint32(len(s.nodes)),
		edgeCount:             uint32(len(s.edges)),
		layoutLinkCount:       uint32(len(renderableLayoutLinks)),
		renderableLayoutLinks: renderableLayoutLinks,
	}
	b.interiorCount = int(b.nodeCount) * BufInteriorSlotsPerNode

	// Port block is self-sizing: total port rows = sum of each node's ports.
	for i := range s.nodes {
		b.portCount += len(s.nodes[i].ports)
	}

	b.labelBytes, b.labelOffs, b.labelLens = s.buildLabelSection()
	b.labelBytesCount = len(b.labelBytes)

	b.portNameBytes, b.portNameOffs, b.portNameLens = s.buildPortNameSection(b.portCount)
	b.portNameBytesCount = len(b.portNameBytes)

	b.edgeLabelBytes, b.edgeLabelOffs, b.edgeLabelLens = s.buildEdgeLabelSection()
	b.edgeLabelBytesCount = len(b.edgeLabelBytes)

	b.eventCount = len(s.pendingEvents)

	b.size = BufHeaderSize +
		int(b.beadCount)*BufBeadStride +
		int(b.nodeCount)*BufNodeStride +
		b.interiorCount*BufInteriorStride +
		int(b.edgeCount)*BufEdgeStride +
		int(b.layoutLinkCount)*BufLayoutLinkStride +
		b.portCount*BufPortStride +
		BufCameraStride +
		BufOverlayStride +
		BufSceneStride +
		b.labelBytesCount +
		b.eventCount*BufEventStride +
		b.portNameBytesCount +
		b.edgeLabelBytesCount

	return b
}

// writeHeader writes the fixed 36-byte header and increments s.tick. Returns the offset after
// the header.
func (s *SnapshotState) writeHeader(buf []byte, b *snapshotBuild) int {
	// Header: [tick][beadCount][nodeCount][edgeCount][portCount][labelBytesCount][eventCount][portNameBytesCount][edgeLabelBytesCount][layoutLinkCount]
	off := 0
	binary.LittleEndian.PutUint32(buf[off:], s.tick)
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], b.beadCount)
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], b.nodeCount)
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], b.edgeCount)
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], uint32(b.portCount))
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], uint32(b.labelBytesCount))
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], uint32(b.eventCount))
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], uint32(b.portNameBytesCount))
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], uint32(b.edgeLabelBytesCount))
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], b.layoutLinkCount)
	off += 4
	s.tick++
	return off
}

// writeBeadBlock writes one row per live bead (map iteration; row order is not stable across
// snapshots, but the renderer reads beads by row position each frame with no cross-frame
// identity needed).
func (s *SnapshotState) writeBeadBlock(buf []byte, off int, b *snapshotBuild) int {
	beadBuf := buf[off : off+int(b.beadCount)*BufBeadStride]
	row := 0
	for _, bead := range s.beads {
		SetBeadRow(beadBuf, row,
			float32(bead.x), float32(bead.y), float32(bead.z),
			int32(bead.value), 1)
		row++
	}
	return off + int(b.beadCount)*BufBeadStride
}

// writeNodeBlock writes the Node block: stable row order (insertion order of node IDs).
func (s *SnapshotState) writeNodeBlock(buf []byte, off int, b *snapshotBuild) int {
	nodeBuf := buf[off : off+int(b.nodeCount)*BufNodeStride]
	for i, n := range s.nodes {
		SetNodeRow(nodeBuf, i,
			float32(n.cx), float32(n.cy), float32(n.cz),
			float32(n.radius), float32(n.sphereR),
			float32(n.vrx), float32(n.vry), float32(n.vrz),
			float32(n.frx), float32(n.fry), float32(n.frz),
			n.evRecv, n.evFire, n.evSend, n.evArrive, n.evDone, n.selected, n.kindID,
			b.labelOffs[i], b.labelLens[i], n.hovered, n.latchedSel)
	}
	return off + int(b.nodeCount)*BufNodeStride
}

// writeInteriorBlock writes FIXED BufInteriorSlotsPerNode rows per node, stable node order
// (row = nodeRow*slotsPerNode + slot). No header count — the decoder derives the length from
// nodeCount. Empty slots are written with present=0 so a popped bead clears on the render side.
func (s *SnapshotState) writeInteriorBlock(buf []byte, off int, b *snapshotBuild) int {
	interiorBuf := buf[off : off+b.interiorCount*BufInteriorStride]
	for i, n := range s.nodes {
		for slot := 0; slot < BufInteriorSlotsPerNode; slot++ {
			it := n.interior[slot]
			SetInteriorRow(interiorBuf, i*BufInteriorSlotsPerNode+slot,
				it.present, it.value,
				float32(it.ox), float32(it.oy), float32(it.oz))
		}
	}
	return off + b.interiorCount*BufInteriorStride
}

// writeEdgeBlock writes the Edge block: stable row order (insertion order of edge labels).
func (s *SnapshotState) writeEdgeBlock(buf []byte, off int, b *snapshotBuild) int {
	edgeBuf := buf[off : off+int(b.edgeCount)*BufEdgeStride]
	for i, e := range s.edges {
		SetEdgeRow(edgeBuf, i,
			float32(e.sx), float32(e.sy), float32(e.sz),
			float32(e.ex), float32(e.ey), float32(e.ez), e.selected,
			b.edgeLabelOffs[i], b.edgeLabelLens[i])
	}
	return off + int(b.edgeCount)*BufEdgeStride
}

// edgeRowForPair returns the buffer edge-row index of the bead edge connecting node ids a/b
// (in either direction), or -1 when no such edge exists. Recomputed fresh every buildSnapshot
// call (not cached at load) so a layout link's overlay segment stays resolved to whichever
// edge row currently connects that pair — the Edge block's SX..EZ are themselves re-emitted
// on every node/port move, so riding along on the row index (rather than duplicating
// endpoints here) is what keeps the overlay attached under a drag.
func (s *SnapshotState) edgeRowForPair(a, b string) int32 {
	for i, e := range s.edges {
		if (e.srcNode == a && e.dstNode == b) || (e.srcNode == b && e.dstNode == a) {
			return int32(i)
		}
	}
	return -1
}

// writeLayoutLinkBlock writes the LayoutLink block: stable row order (insertion order of the
// first-seen pair). Sourced from the LAYOUT model (LocalPolars), independent of the Edge block
// for the PAIR itself; EdgeRow is resolved against the CURRENT Edge block each call (see
// edgeRowForPair) so the overlay segment always draws along the live port-anchored edge
// endpoints, not stale or center-anchored ones — -1 when the pair has no bead edge (renderer
// fallback: node centers).
func (s *SnapshotState) writeLayoutLinkBlock(buf []byte, off int, b *snapshotBuild) int {
	llBuf := buf[off : off+int(b.layoutLinkCount)*BufLayoutLinkStride]
	// Iterates b.renderableLayoutLinks (pre-filtered by resolvableLayoutLinks), NOT
	// s.layoutLinks — so nodeRowIndex is guaranteed >= 0 for both endpoints and the
	// packed SrcNodeRow/DstNodeRow are always valid node rows. EdgeRow may still be -1
	// (the declared node-centers fallback), whose consumers now read valid endpoint rows.
	for i, ll := range b.renderableLayoutLinks {
		SetLayoutLinkRow(llBuf, i,
			int32(s.nodeRowIndex(ll.srcNode)), int32(s.nodeRowIndex(ll.dstNode)),
			s.edgeRowForPair(ll.srcNode, ll.dstNode))
	}
	return off + int(b.layoutLinkCount)*BufLayoutLinkStride
}

// resolvableLayoutLinks returns the layout-link pairs whose BOTH endpoints resolve to a live
// node row in the CURRENT snapshot. A pair with an unresolvable endpoint (nodeRowIndex == -1)
// is dropped here so it never reaches the buffer: the LayoutLink block's SrcNodeRow/DstNodeRow
// are consumed UNCONDITIONALLY by the renderer's EdgeRow==-1 fallback (node centers), so a -1
// row there is an out-of-bounds read, not a drawable segment. Endpoints always resolve under
// the current build order (every node row is registered before layout links emit); this filter
// makes that a guarantee of the emitted bytes rather than an unenforced assumption. Returns a
// fresh backing slice — never aliases or mutates s.layoutLinks. A drop is surfaced on the
// DEBUG BREADCRUMB channel, but only when the dropped COUNT changes from the prior build —
// this path runs hundreds of times/sec, so a per-build breadcrumb would flood the channel.
func (s *SnapshotState) resolvableLayoutLinks() []layoutLinkSnapState {
	out := s.layoutLinks[:0:0] // len 0, cap 0: the first append allocates, leaving s.layoutLinks intact
	for _, ll := range s.layoutLinks {
		if s.nodeRowIndex(ll.srcNode) >= 0 && s.nodeRowIndex(ll.dstNode) >= 0 {
			out = append(out, ll)
		}
	}
	if dropped := len(s.layoutLinks) - len(out); dropped != s.lastDroppedLayoutLinks {
		if s.breadcrumb != nil {
			s.breadcrumb("layout-link-unresolvable-endpoint", "", "",
				fmt.Sprintf("dropped=%d kept=%d total=%d", dropped, len(out), len(s.layoutLinks)))
		}
		s.lastDroppedLayoutLinks = dropped
	}
	return out
}

// SetBreadcrumbSink wires the DEBUG BREADCRUMB channel (tr.Breadcrumb in production, nil in
// headless tests). Called once at construction from the same goroutine that drives Update.
func (s *SnapshotState) SetBreadcrumbSink(f func(label, node, port, value string)) {
	s.breadcrumb = f
}

// writePortBlock writes the Port block: flattened over nodes in stable node-row order — for
// each node in its buffer row order, that node's ports in node-geometry Ports order. NodeRow
// is the owning node's row index; DX/DY/DZ is the port surface direction; IsInput marks input
// ports. The Go-side port-row table (LookupPortRow) is built in this identical flattened
// order, so port row i ↔ (node, port) i for hit resolution.
func (s *SnapshotState) writePortBlock(buf []byte, off int, b *snapshotBuild) int {
	portBuf := buf[off : off+b.portCount*BufPortStride]
	prow := 0
	for i := range s.nodes {
		for _, p := range s.nodes[i].ports {
			SetPortRow(portBuf, prow,
				int32(i), float32(p.dx), float32(p.dy), float32(p.dz),
				float32(p.px), float32(p.py), float32(p.pz), boolU8(p.isInput), p.hovered,
				b.portNameOffs[prow], b.portNameLens[prow])
			prow++
		}
	}
	return off + b.portCount*BufPortStride
}

// writeCameraBlock writes the Camera block (always 1 row).
func (s *SnapshotState) writeCameraBlock(buf []byte, off int) int {
	c := s.camera
	SetCameraRow(buf[off:],
		float32(c.px), float32(c.py), float32(c.pz),
		float32(c.r),
		float32(c.posTheta), float32(c.posPhi),
		float32(c.upTheta), float32(c.upPhi))
	return off + BufCameraStride
}

// writeOverlayBlock writes the Overlay block (always 1 row).
func (s *SnapshotState) writeOverlayBlock(buf []byte, off int) int {
	ov := s.overlay
	SetOverlayRow(buf[off:],
		ov.sceneTori, ov.scenePoles, ov.nodePoles,
		ov.selSpherePoles, ov.handholds,
		ov.labelsGlobal,
		ov.overlaysVis, ov.doubleLinks)
	return off + BufOverlayStride
}

// writeSceneBlock writes the Scene block (always 1 row): the persisted scene-sphere center +
// radius, established once at load and never moved (see KindSceneSphere / sceneSnapState).
func (s *SnapshotState) writeSceneBlock(buf []byte, off int) int {
	sc := s.scene
	SetSceneRow(buf[off:], float32(sc.cx), float32(sc.cy), float32(sc.cz), float32(sc.radius))
	return off + BufSceneStride
}

// writeLabelBytesSection writes the Label bytes section (self-sizing via header
// labelBytesCount): every node's label UTF-8 bytes concatenated in node-row order. Each
// node's LabelOff/LabelLen columns slice into this section; the numeric node row carries its
// human label with no sidecar.
func (s *SnapshotState) writeLabelBytesSection(buf []byte, off int, b *snapshotBuild) int {
	copy(buf[off:off+b.labelBytesCount], b.labelBytes)
	return off + b.labelBytesCount
}

// writeEventBlockSection writes the EVENT block (self-sizing via header eventCount): the
// per-tick causal trace events (numeric rows + string-section refs). Consumed only by the
// ext-host .probe logger.
func (s *SnapshotState) writeEventBlockSection(buf []byte, off int, b *snapshotBuild) int {
	eventBuf := buf[off : off+b.eventCount*BufEventStride]
	s.writeEventBlock(eventBuf, s.portRowLookup())
	return off + b.eventCount*BufEventStride
}

// writePortNameBytesSection writes the Port-name bytes section (self-sizing via header
// portNameBytesCount): every port's name UTF-8 bytes in flattened port-row order;
// PortNameOff/PortNameLen slice into it.
func (s *SnapshotState) writePortNameBytesSection(buf []byte, off int, b *snapshotBuild) int {
	copy(buf[off:off+b.portNameBytesCount], b.portNameBytes)
	return off + b.portNameBytesCount
}

// writeEdgeLabelBytesSection writes the Edge-label bytes section (self-sizing via header
// edgeLabelBytesCount): every edge's label UTF-8 bytes in edge-row order;
// EdgeLabelOff/EdgeLabelLen slice into it.
func (s *SnapshotState) writeEdgeLabelBytesSection(buf []byte, off int, b *snapshotBuild) int {
	copy(buf[off:off+b.edgeLabelBytesCount], b.edgeLabelBytes)
	return off + b.edgeLabelBytesCount
}

// buildSnapshot packs all current state into one snapshot []byte. It is a short orchestrator:
// newSnapshotBuild computes all counts/string-sections once, then each block is written
// in the exact byte-layout order the header/format comment at the top of this file documents.
func (s *SnapshotState) buildSnapshot() []byte {
	b := s.newSnapshotBuild()
	buf := make([]byte, b.size)

	off := s.writeHeader(buf, b)
	off = s.writeBeadBlock(buf, off, b)
	off = s.writeNodeBlock(buf, off, b)
	off = s.writeInteriorBlock(buf, off, b)
	off = s.writeEdgeBlock(buf, off, b)
	off = s.writeLayoutLinkBlock(buf, off, b)
	off = s.writePortBlock(buf, off, b)
	off = s.writeCameraBlock(buf, off)
	off = s.writeOverlayBlock(buf, off)
	off = s.writeSceneBlock(buf, off)
	off = s.writeLabelBytesSection(buf, off, b)
	off = s.writeEventBlockSection(buf, off, b)
	off = s.writePortNameBytesSection(buf, off, b)
	s.writeEdgeLabelBytesSection(buf, off, b)

	return buf
}

func boolU8(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}
