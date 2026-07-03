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
//	Header   24 bytes: [tick:u32][beadCount:u32][nodeCount:u32][edgeCount:u32][portCount:u32][labelBytesCount:u32]
//	Bead     beadCount × BufBeadStride bytes
//	Node     nodeCount × BufNodeStride bytes   (persistent geom + transient event flags + label off/len)
//	Interior nodeCount × BufInteriorSlotsPerNode × BufInteriorStride bytes
//	Edge     edgeCount × BufEdgeStride bytes
//	Port     portCount × BufPortStride bytes   (flattened over nodes in node-row order)
//	Camera   BufCameraStride bytes             (always 1 row)
//	Overlay  BufOverlayStride bytes            (always 1 row)
//	Label    labelBytesCount bytes             (node labels' UTF-8 bytes, node-row order)
//
// At the rollout flip (a later phase), this becomes the sole framed stdout once
// the JSON trace is removed. For now it runs alongside JSON on a side channel.
//
// All SnapshotState methods must be called from a single goroutine (the Trace
// drain goroutine). No internal synchronisation.

package Buffer

import (
	"encoding/binary"
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

	// Live in-flight beads, keyed by (sourceNode, sourcePort, gen).
	beads map[beadSnapKey]beadSnapState

	// srcToDest maps (sourceNode, sourcePort) → destination node ID.
	// Populated from KindSend Target fields so arrive events can route EvArrive
	// to the correct destination node.
	srcToDest map[srcPortKey]string

	// directlyFadedNodes / directlyFadedEdges are the Go-owned fade SEED sets, mirrored
	// from MoveDispatch via KindFade events (node ids / edge labels the user toggled faded).
	// buildSnapshot runs the computeFade fixpoint over these seeds + the current adjacency
	// each build and writes the resulting Faded columns; a faded edge's transit beads are
	// written Live=0. Empty = nothing faded.
	directlyFadedNodes map[string]bool
	directlyFadedEdges map[string]bool

	// Camera and overlay singletons (always one row each in the snapshot).
	camera  cameraSnapState
	overlay overlaySnapState

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
	torusRed      uint8
	missVal       int32
	mx, my, mz    float64
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
	isInput    bool
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

// beadSnapState holds current position + metadata for one in-flight bead.
type beadSnapState struct {
	x, y, z float64
	value   int
	frac    float64
	beadID  uint64
}

// cameraSnapState mirrors the camera block (single row).
type cameraSnapState struct {
	px, py, pz       float64
	r                float64
	posTheta, posPhi float64
	upTheta, upPhi   float64
}

// overlaySnapState mirrors the overlay block (single row).
type overlaySnapState struct {
	sceneTori      uint8
	scenePoles     uint8
	nodePoles      uint8
	angleLabels    uint8
	selSpherePoles uint8
	handholds      uint8
	labelsGlobal   uint8
	badgesGlobal   uint8
	overlaysVis    uint8
	doubleLinks    uint8
	// selMode is the current select mode (1 = own, 0 = surface), set by KindSelect.
	// Not an overlay flag — rides the overlay singleton row for the on-surface highlight.
	selMode uint8
}

// NewSnapshotState creates an empty SnapshotState that writes framed snapshots
// to out. Pass nil for out to build snapshots without emitting them (useful in
// tests that only inspect state).
func NewSnapshotState(out io.Writer) *SnapshotState {
	return &SnapshotState{
		nodeIndex:          map[string]int{},
		edgeIndex:          map[string]int{},
		beads:              map[beadSnapKey]beadSnapState{},
		srcToDest:          map[srcPortKey]string{},
		directlyFadedNodes: map[string]bool{},
		directlyFadedEdges: map[string]bool{},
		out:                out,
	}
}

// Update processes one trace event, updating the snapshot state.
// Must be called from the Trace drain goroutine on every event.
// On KindPosition events it also triggers a snapshot emit.
func (s *SnapshotState) Update(ev T.Event) {
	switch ev.Kind {
	case T.KindNodeGeometry:
		s.onNodeGeometry(ev)
		s.emitSnapshot() // state-change point: emit on geometry updates
	case T.KindGeometry:
		s.onEdgeGeometry(ev)
		s.emitSnapshot() // state-change point: emit on edge geometry updates
	case T.KindNodeStatus:
		s.onNodeStatus(ev)
		s.emitSnapshot() // state-change point: emit on status changes
	case T.KindCamera:
		s.camera = cameraSnapState{
			px: ev.PX, py: ev.PY, pz: ev.PZ,
			r:        ev.R,
			posTheta: ev.PosTheta, posPhi: ev.PosPhi,
			upTheta: ev.UpTheta, upPhi: ev.UpPhi,
		}
		s.emitSnapshot() // state-change point: emit on camera changes
	case T.KindSceneTori:
		s.overlay.sceneTori = boolU8(ev.Visible)
		s.emitSnapshot()
	case T.KindScenePoles:
		s.overlay.scenePoles = boolU8(ev.Visible)
		s.emitSnapshot()
	case T.KindNodePoles:
		s.overlay.nodePoles = boolU8(ev.Visible)
		s.emitSnapshot()
	case T.KindAngleLabels:
		s.overlay.angleLabels = boolU8(ev.Visible)
		s.emitSnapshot()
	case T.KindSelSpherePoles:
		s.overlay.selSpherePoles = boolU8(ev.Visible)
		s.emitSnapshot()
	case T.KindHandholds:
		s.overlay.handholds = boolU8(ev.Visible)
		s.emitSnapshot()
	case T.KindLabelsGlobal:
		s.overlay.labelsGlobal = boolU8(ev.Visible)
		s.emitSnapshot()
	case T.KindBadgesGlobal:
		s.overlay.badgesGlobal = boolU8(ev.Visible)
		s.emitSnapshot()
	case T.KindOverlaysVis:
		s.overlay.overlaysVis = boolU8(ev.Visible)
		s.emitSnapshot()
	case T.KindDoubleLinks:
		s.overlay.doubleLinks = boolU8(ev.Visible)
		s.emitSnapshot()

	case T.KindPosition:
		// Update the live bead state AND trigger a snapshot on the position cadence (~16ms).
		k := beadSnapKey{ev.Node, ev.Port, ev.Bead}
		s.beads[k] = beadSnapState{
			x: ev.X, y: ev.Y, z: ev.Z,
			value:  ev.Value,
			frac:   ev.F,
			beadID: ev.Bead,
		}
		s.emitSnapshot()

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
			// Edge selection has no surface/own owner set; reset the mode to surface.
			s.overlay.selMode = 0
		} else {
			s.setSelected(ev.Node)
			// Value carries the select mode (1 = own, 0 = surface); store it for the
			// on-surface highlight. Cleared to surface when the selection is cleared.
			if ev.Node == "" {
				s.overlay.selMode = 0
			} else {
				s.overlay.selMode = uint8(ev.Value)
			}
		}
		s.emitSnapshot()

	case T.KindFade:
		// Go-owned fade seeds: replace the mirrored directly-faded sets with the full seed
		// lists MoveDispatch just emitted, then emit so buildSnapshot recomputes the fade
		// fixpoint and refreshes the Faded columns. Empty lists clear all fade.
		s.directlyFadedNodes = sliceToSet(ev.FadedNodes)
		s.directlyFadedEdges = sliceToSet(ev.FadedEdges)
		s.emitSnapshot()
	}
}

// sliceToSet builds a membership set from a slice of ids (nil → empty set).
func sliceToSet(ids []string) map[string]bool {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

// NodeCount returns the number of registered nodes (for tests).
func (s *SnapshotState) NodeCount() int { return len(s.nodes) }

// EdgeCount returns the number of registered edges (for tests).
func (s *SnapshotState) EdgeCount() int { return len(s.edges) }

// BeadCount returns the number of live in-flight beads (for tests).
func (s *SnapshotState) BeadCount() int { return len(s.beads) }

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
	n.ports = n.ports[:0]
	for _, p := range ev.Ports {
		n.ports = append(n.ports, portSnapState{name: p.Name, dx: p.DX, dy: p.DY, dz: p.DZ, isInput: p.IsInput})
	}
	// Republish the port-row table: ports (and node order) just changed. Built in the SAME
	// flattened order buildSnapshot writes the Port block, so port row i ↔ entry i.
	s.rebuildPortTable()
	// Status fields (torusRed, missVal, mx/my/mz) are preserved across geometry
	// re-emits so a node-move does not silently clear an active error state.
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

func (s *SnapshotState) onNodeStatus(ev T.Event) {
	idx, ok := s.nodeIndex[ev.Node]
	if !ok {
		return
	}
	n := &s.nodes[idx]
	n.torusRed = boolU8(ev.TorusRed)
	if ev.TorusRed {
		n.missVal = int32(ev.Value)
		n.mx, n.my, n.mz = ev.X, ev.Y, ev.Z
	} else {
		n.missVal = 0
		n.mx, n.my, n.mz = 0, 0, 0
	}
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
		} else {
			s.nodes[i].selected = 0
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
}

// emitSnapshot builds one snapshot, writes a framed frame to s.out, then
// clears transient event flags. Ignores write errors (nothing reads this fd
// yet — on-but-harmless until the rollout flip phase).
func (s *SnapshotState) emitSnapshot() {
	snap := s.buildSnapshot()
	if s.out != nil {
		var hdr [4]byte
		binary.LittleEndian.PutUint32(hdr[:], uint32(len(snap)))
		_, _ = s.out.Write(hdr[:])
		_, _ = s.out.Write(snap)
	}
	s.clearTransients()
}

// buildSnapshot packs all current state into one snapshot []byte.
func (s *SnapshotState) buildSnapshot() []byte {
	beadCount := uint32(len(s.beads))
	nodeCount := uint32(len(s.nodes))
	edgeCount := uint32(len(s.edges))

	// Fade fixpoint: recompute the faded node/edge sets from the Go-owned seeds + the current
	// adjacency (computeFade). The result drives the Node/Edge Faded columns and suppresses
	// faded edges' transit beads. fadedNodePairs indexes faded edges by (srcNode,dstNode) so a
	// live bead (routed sourceNode→dest via srcToDest) can be matched to a faded edge.
	fadeEdges := make([]FadeEdge, len(s.edges))
	for i := range s.edges {
		fadeEdges[i] = FadeEdge{ID: s.edgeLabels[i], Source: s.edges[i].srcNode, Target: s.edges[i].dstNode}
	}
	fadedNodes, fadedEdges := computeFade(s.nodeIDs, fadeEdges, s.directlyFadedNodes, s.directlyFadedEdges)
	fadedNodePairs := map[srcPortKey]bool{} // reuse {node,port} as {src,dst} pair holder
	for i := range s.edges {
		if fadedEdges[s.edgeLabels[i]] {
			fadedNodePairs[srcPortKey{s.edges[i].srcNode, s.edges[i].dstNode}] = true
		}
	}

	interiorCount := int(nodeCount) * BufInteriorSlotsPerNode

	// Port block is self-sizing: total port rows = sum of each node's ports.
	portCount := 0
	for i := range s.nodes {
		portCount += len(s.nodes[i].ports)
	}

	// Label section is self-sizing (like the Port block): concatenate every node's label
	// UTF-8 bytes in node-row order; each node's LabelOff/LabelLen columns slice into it.
	// labelBytes is the trailing section; labelOffs/labelLens are the per-row columns.
	labelBytes := make([]byte, 0, int(nodeCount)*8)
	labelOffs := make([]uint32, nodeCount)
	labelLens := make([]uint32, nodeCount)
	for i := range s.nodes {
		labelOffs[i] = uint32(len(labelBytes))
		lb := []byte(s.nodes[i].label)
		labelLens[i] = uint32(len(lb))
		labelBytes = append(labelBytes, lb...)
	}
	labelBytesCount := len(labelBytes)

	size := BufHeaderSize +
		int(beadCount)*BufBeadStride +
		int(nodeCount)*BufNodeStride +
		interiorCount*BufInteriorStride +
		int(edgeCount)*BufEdgeStride +
		portCount*BufPortStride +
		BufCameraStride +
		BufOverlayStride +
		labelBytesCount

	buf := make([]byte, size)

	// Header: [tick:u32][beadCount:u32][nodeCount:u32][edgeCount:u32][portCount:u32][labelBytesCount:u32]
	off := 0
	binary.LittleEndian.PutUint32(buf[off:], s.tick)
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], beadCount)
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], nodeCount)
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], edgeCount)
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], uint32(portCount))
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], uint32(labelBytesCount))
	off += 4
	s.tick++

	// Bead block: one row per live bead (map iteration; order not guaranteed,
	// but the consumer identifies beads by beadID, not row position).
	beadBuf := buf[off : off+int(beadCount)*BufBeadStride]
	row := 0
	for k, b := range s.beads {
		// Suppress a faded edge's transit bead: a faded edge shows no traveling bead
		// (pre-branch SingleEdgeTube `!faded && <PulseBead/>`). The bead's edge is its
		// route sourceNode→dest (dest via srcToDest); if that edge is faded, write the row
		// Live=0 so the renderer hides it (buffer beads are edge-agnostic on the TS side).
		live := uint8(1)
		if dest, ok := s.srcToDest[srcPortKey{k.node, k.port}]; ok && fadedNodePairs[srcPortKey{k.node, dest}] {
			live = 0
		}
		SetBeadRow(beadBuf, row,
			float32(b.x), float32(b.y), float32(b.z),
			int32(b.value), float32(b.frac), uint32(b.beadID), live)
		row++
	}
	off += int(beadCount) * BufBeadStride

	// Node block: stable row order (insertion order of node IDs).
	nodeBuf := buf[off : off+int(nodeCount)*BufNodeStride]
	for i, n := range s.nodes {
		SetNodeRow(nodeBuf, i,
			float32(n.cx), float32(n.cy), float32(n.cz),
			float32(n.radius), float32(n.sphereR),
			float32(n.vrx), float32(n.vry), float32(n.vrz),
			float32(n.frx), float32(n.fry), float32(n.frz),
			n.torusRed, n.missVal,
			float32(n.mx), float32(n.my), float32(n.mz),
			n.evRecv, n.evFire, n.evSend, n.evArrive, n.evDone, n.selected, n.kindID,
			labelOffs[i], labelLens[i], boolU8(fadedNodes[s.nodeIDs[i]]))
	}
	off += int(nodeCount) * BufNodeStride

	// Interior block: FIXED BufInteriorSlotsPerNode rows per node, stable node order
	// (row = nodeRow*slotsPerNode + slot). No header count — the decoder derives the
	// length from nodeCount. Empty slots are written with present=0 so a popped bead
	// clears on the render side.
	interiorBuf := buf[off : off+interiorCount*BufInteriorStride]
	for i, n := range s.nodes {
		for slot := 0; slot < BufInteriorSlotsPerNode; slot++ {
			it := n.interior[slot]
			SetInteriorRow(interiorBuf, i*BufInteriorSlotsPerNode+slot,
				it.present, it.value,
				float32(it.ox), float32(it.oy), float32(it.oz))
		}
	}
	off += interiorCount * BufInteriorStride

	// Edge block: stable row order (insertion order of edge labels).
	edgeBuf := buf[off : off+int(edgeCount)*BufEdgeStride]
	for i, e := range s.edges {
		SetEdgeRow(edgeBuf, i,
			float32(e.sx), float32(e.sy), float32(e.sz),
			float32(e.ex), float32(e.ey), float32(e.ez),
			int32(s.nodeRowIndex(e.srcNode)), int32(s.nodeRowIndex(e.dstNode)), e.selected,
			boolU8(fadedEdges[s.edgeLabels[i]]))
	}
	off += int(edgeCount) * BufEdgeStride

	// Port block: flattened over nodes in stable node-row order — for each node in its
	// buffer row order, that node's ports in node-geometry Ports order. NodeRow is the
	// owning node's row index; DX/DY/DZ is the port surface direction; IsInput marks input
	// ports. The Go-side port-row table (LookupPortRow) is built in this identical
	// flattened order, so port row i ↔ (node, port) i for hit resolution.
	portBuf := buf[off : off+portCount*BufPortStride]
	prow := 0
	for i := range s.nodes {
		for _, p := range s.nodes[i].ports {
			SetPortRow(portBuf, prow,
				int32(i), float32(p.dx), float32(p.dy), float32(p.dz), boolU8(p.isInput))
			prow++
		}
	}
	off += portCount * BufPortStride

	// Camera block (always 1 row).
	c := s.camera
	SetCameraRow(buf[off:],
		float32(c.px), float32(c.py), float32(c.pz),
		float32(c.r),
		float32(c.posTheta), float32(c.posPhi),
		float32(c.upTheta), float32(c.upPhi))
	off += BufCameraStride

	// Overlay block (always 1 row).
	ov := s.overlay
	SetOverlayRow(buf[off:],
		ov.sceneTori, ov.scenePoles, ov.nodePoles, ov.angleLabels,
		ov.selSpherePoles, ov.handholds,
		ov.labelsGlobal, ov.badgesGlobal,
		ov.overlaysVis, ov.doubleLinks, ov.selMode)
	off += BufOverlayStride

	// Label bytes section (self-sizing via header labelBytesCount): every node's label
	// UTF-8 bytes concatenated in node-row order. Each node's LabelOff/LabelLen columns
	// slice into this section; the numeric node row carries its human label with no sidecar.
	copy(buf[off:off+labelBytesCount], labelBytes)

	return buf
}

func boolU8(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}
