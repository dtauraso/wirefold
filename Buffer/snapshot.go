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
//	Header   16 bytes: [tick:u32][beadCount:u32][nodeCount:u32][edgeCount:u32]
//	Bead     beadCount × BufBeadStride bytes
//	Node     nodeCount × BufNodeStride bytes   (persistent geom + transient event flags)
//	Interior nodeCount × BufInteriorSlotsPerNode × BufInteriorStride bytes
//	Edge     edgeCount × BufEdgeStride bytes
//	Camera   BufCameraStride bytes             (always 1 row)
//	Overlay  BufOverlayStride bytes            (always 1 row)
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

	T "github.com/dtauraso/wirefold/Trace"
)

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

	// Camera and overlay singletons (always one row each in the snapshot).
	camera  cameraSnapState
	overlay overlaySnapState

	// tick is the monotonic snapshot sequence counter.
	tick uint32

	// out receives framed binary snapshots. Nil = silent discard.
	out io.Writer
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
	torusRed        uint8
	missVal         int32
	mx, my, mz      float64
	evRecv          uint8
	evFire          uint8
	evSend          uint8
	evArrive        uint8
	evDone          uint8
	// selected is PERSISTENT (not a transient event flag): 1 marks this node as the
	// current click-selected node. Set/cleared by KindSelect; NOT reset in clearTransients.
	selected uint8
	// interior holds this node's 2x2 held/interior-bead grid (slot = row*2 + col).
	// PERSISTENT — a slot keeps its state until the next KindNodeBead updates it
	// (present=false explicitly clears a popped slot). Not touched by clearTransients.
	interior [BufInteriorSlotsPerNode]interiorSlotState
}

// interiorSlotState holds one interior grid slot's present/value + Go-owned
// NODE-LOCAL offset (relative to the node center).
type interiorSlotState struct {
	present    uint8
	value      int32
	ox, oy, oz float64
}

// edgeSnapState holds persistent segment endpoints for one edge.
type edgeSnapState struct {
	sx, sy, sz float64
	ex, ey, ez float64
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
}

// NewSnapshotState creates an empty SnapshotState that writes framed snapshots
// to out. Pass nil for out to build snapshots without emitting them (useful in
// tests that only inspect state).
func NewSnapshotState(out io.Writer) *SnapshotState {
	return &SnapshotState{
		nodeIndex: map[string]int{},
		edgeIndex: map[string]int{},
		beads:     map[beadSnapKey]beadSnapState{},
		srcToDest: map[srcPortKey]string{},
		out:       out,
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
		// Go-owned selection: mark ev.Node selected, clear every other node. ev.Node==""
		// clears the selection entirely. Persistent — survives across snapshots until the
		// next select. Emit so the change is reflected in the buffer immediately.
		s.setSelected(ev.Node)
		s.emitSnapshot()
	}
}

// NodeCount returns the number of registered nodes (for tests).
func (s *SnapshotState) NodeCount() int { return len(s.nodes) }

// EdgeCount returns the number of registered edges (for tests).
func (s *SnapshotState) EdgeCount() int { return len(s.edges) }

// BeadCount returns the number of live in-flight beads (for tests).
func (s *SnapshotState) BeadCount() int { return len(s.beads) }

// BuildSnapshot exposes the snapshot builder for tests.
func (s *SnapshotState) BuildSnapshot() []byte { return s.buildSnapshot() }

// --- internal helpers --------------------------------------------------------

func (s *SnapshotState) onNodeGeometry(ev T.Event) {
	id := ev.Node
	if _, exists := s.nodeIndex[id]; !exists {
		s.nodeIndex[id] = len(s.nodeIDs)
		s.nodeIDs = append(s.nodeIDs, id)
		s.nodes = append(s.nodes, nodeSnapState{})
	}
	idx := s.nodeIndex[id]
	n := &s.nodes[idx]
	n.cx, n.cy, n.cz = ev.NX, ev.NY, ev.NZ
	n.radius = ev.Radius
	n.sphereR = ev.SphereR
	// Status fields (torusRed, missVal, mx/my/mz) are preserved across geometry
	// re-emits so a node-move does not silently clear an active error state.
}

func (s *SnapshotState) onEdgeGeometry(ev T.Event) {
	label := ev.Edge
	if _, exists := s.edgeIndex[label]; !exists {
		s.edgeIndex[label] = len(s.edgeLabels)
		s.edgeLabels = append(s.edgeLabels, label)
		s.edges = append(s.edges, edgeSnapState{})
	}
	idx := s.edgeIndex[label]
	e := &s.edges[idx]
	e.sx, e.sy, e.sz = ev.SX, ev.SY, ev.SZ
	e.ex, e.ey, e.ez = ev.EX, ev.EY, ev.EZ
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

	interiorCount := int(nodeCount) * BufInteriorSlotsPerNode

	size := BufHeaderSize +
		int(beadCount)*BufBeadStride +
		int(nodeCount)*BufNodeStride +
		interiorCount*BufInteriorStride +
		int(edgeCount)*BufEdgeStride +
		BufCameraStride +
		BufOverlayStride

	buf := make([]byte, size)

	// Header: [tick:u32][beadCount:u32][nodeCount:u32][edgeCount:u32]
	off := 0
	binary.LittleEndian.PutUint32(buf[off:], s.tick)
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], beadCount)
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], nodeCount)
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], edgeCount)
	off += 4
	s.tick++

	// Bead block: one row per live bead (map iteration; order not guaranteed,
	// but the consumer identifies beads by beadID, not row position).
	beadBuf := buf[off : off+int(beadCount)*BufBeadStride]
	row := 0
	for _, b := range s.beads {
		SetBeadRow(beadBuf, row,
			float32(b.x), float32(b.y), float32(b.z),
			int32(b.value), float32(b.frac), uint32(b.beadID), 1)
		row++
	}
	off += int(beadCount) * BufBeadStride

	// Node block: stable row order (insertion order of node IDs).
	nodeBuf := buf[off : off+int(nodeCount)*BufNodeStride]
	for i, n := range s.nodes {
		SetNodeRow(nodeBuf, i,
			float32(n.cx), float32(n.cy), float32(n.cz),
			float32(n.radius), float32(n.sphereR),
			n.torusRed, n.missVal,
			float32(n.mx), float32(n.my), float32(n.mz),
			n.evRecv, n.evFire, n.evSend, n.evArrive, n.evDone, n.selected)
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
			float32(e.ex), float32(e.ey), float32(e.ez))
	}
	off += int(edgeCount) * BufEdgeStride

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
		ov.overlaysVis, ov.doubleLinks)

	return buf
}

func boolU8(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}
