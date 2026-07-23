// Buffer/pack.go — the PACK half of the snapshot pipeline: buildSnapshot and its block-writer
// helpers turn the accumulated SnapshotState into the framed binary bytes described by the
// layout comment atop snapshot.go. snapshot.go owns INGEST (Update, the per-kind on* handlers,
// selection/hover mutation, row-table rebuilds, emitSnapshot/eventReady); this file is pure
// packing — it reads SnapshotState fields but never mutates ingest state. Split out because the
// two halves see very different commit traffic: pack.go absorbs nearly every buffer-layout/column
// change, while snapshot.go changes only when a new trace event kind needs handling. Same
// package, same receiver (*SnapshotState) — a pure file move, mirroring the split already
// established by snapshot_events.go.

package Buffer

import (
	"encoding/binary"
	"fmt"
)

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
	// Either/or (memory/feedback_no_single_writer_bridge.md): once the dedicated per-node
	// streams are active, each node's OWN layout-links stream on its own fd
	// (Buffer.BuildNodeStreamFrame's LayoutLink section) — the fd-3 scene frame's
	// LayoutLink block is never double-sourced alongside it. Falls back to the fd-3 block
	// (renderableLayoutLinks non-empty) when node streams are not active (env unset —
	// headless tests, non-extension launches).
	var renderableLayoutLinks []layoutLinkSnapState
	if !s.nodeStreamActive.Load() {
		renderableLayoutLinks = s.resolvableLayoutLinks()
	}
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
			n.selected, n.kindID,
			b.labelOffs[i], b.labelLens[i], n.hovered, n.latchedSel, n.gotDragMsg,
			n.dragDeltaA, n.dragDeltaB, n.dragDeltaC)
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
// The endpoint coordinates are DERIVED from the same per-node Port block data
// (SnapshotState.portWorldPos), not read from e.sx..ez directly: the node's port
// geometry and the edge's own last emitted segment can straddle different buildSnapshot
// frames during a continuous drag (different goroutines), which perpetually lags the
// rendered edge endpoint one drag-step behind the port sphere it should be pinned to.
// Deriving from the Port block instead makes a node-geometry update move the port AND
// the edge endpoint in the SAME frame, unconditionally. e.sx..ez remain the FALLBACK for
// an edge whose endpoint port hasn't resolved yet (edges can register before their
// endpoint nodes do, see onEdgeGeometry).
func (s *SnapshotState) writeEdgeBlock(buf []byte, off int, b *snapshotBuild) int {
	edgeBuf := buf[off : off+int(b.edgeCount)*BufEdgeStride]
	for i, e := range s.edges {
		sx, sy, sz := e.sx, e.sy, e.sz
		if px, py, pz, ok := s.portWorldPos(e.srcNode, e.srcPort, false); ok {
			sx, sy, sz = px, py, pz
		}
		ex, ey, ez := e.ex, e.ey, e.ez
		if px, py, pz, ok := s.portWorldPos(e.dstNode, e.dstPort, true); ok {
			ex, ey, ez = px, py, pz
		}
		SetEdgeRow(edgeBuf, i,
			float32(sx), float32(sy), float32(sz),
			float32(ex), float32(ey), float32(ez), e.selected,
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

// writeOverlayBlock writes the Overlay block (always 1 row). s.overlay IS the
// OverlayRow value SetOverlayRow writes — no per-field positional arg list at this
// call site, so there is nothing here to transpose, EXCEPT AbcDragCount: when
// s.abcDragCountFor is set (see its doc comment), it overrides s.overlay.AbcDragCount
// with the MoveDispatch-owned count for this one write, leaving s.overlay itself
// untouched (so the fd-3 fallback, when active, still uses its own copy).
func (s *SnapshotState) writeOverlayBlock(buf []byte, off int) int {
	row := s.overlay
	if s.abcDragCountFor != nil {
		row.AbcDragCount = s.abcDragCountFor()
	}
	SetOverlayRow(buf[off:], row)
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
