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

	T "github.com/dtauraso/wirefold/Trace"
)

// snapshotBuild holds all the per-build derived data (counts, string sections,
// total size) that buildSnapshot's block-writer helpers read from. Computed once per build by
// newSnapshotBuild; the block writers never recompute it. The SCENE frame no longer carries
// the node-owner-group blocks (Node/Interior/Port + Label/PortName bytes — see nodeFrameBuild
// for those) or the Edge block + edge-label bytes (see edgeFrameBuild for those).
type snapshotBuild struct {
	layoutLinkCount uint32
	// renderableLayoutLinks is the layout-link pairs whose BOTH endpoints resolve to a live
	// node row this build (resolvableLayoutLinks). layoutLinkCount == len(this), and
	// writeLayoutLinkBlock iterates THIS — never s.layoutLinks — so an unresolvable endpoint
	// is filtered before emit and a -1 SrcNodeRow/DstNodeRow can never reach the buffer.
	renderableLayoutLinks []layoutLinkSnapState

	size int
}

// edgeFrameBuild holds the per-build derived data (counts, string sections, total post-header
// frame size) that buildEdgeFrame's block-writer helpers read from. Computed once per build by
// newEdgeFrameBuild; mirrors nodeFrameBuild's role but for the Edge block + edge-label bytes,
// which travel in their own tagged frame (BufBlockTagEdge).
type edgeFrameBuild struct {
	edgeCount uint32

	edgeLabelBytes      []byte
	edgeLabelOffs       []uint32
	edgeLabelLens       []uint32
	edgeLabelBytesCount int

	size int
}

// nodeFrameBuild holds the per-build derived data (counts, string sections, total size) that
// buildNodeFrame's block-writer helpers read from. Computed once per build by
// newNodeFrameBuild; mirrors snapshotBuild's role but for the Node/Interior/Port blocks +
// Label/PortName bytes, which travel in their own tagged frame (BufBlockTagNode) — these
// three blocks share one owner group (the node movers).
type nodeFrameBuild struct {
	nodeCount                uint32
	interiorCount, portCount int

	labelBytes      []byte
	labelOffs       []uint32
	labelLens       []uint32
	labelBytesCount int

	portNameBytes      []byte
	portNameOffs       []uint32
	portNameLens       []uint32
	portNameBytesCount int

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
		layoutLinkCount:       uint32(len(renderableLayoutLinks)),
		renderableLayoutLinks: renderableLayoutLinks,
	}

	b.size = BufHeaderSize +
		int(b.layoutLinkCount)*BufLayoutLinkStride
	// Either/or (see buildSnapshot's doc comment): camera/overlay/scene are embedded in
	// the scene frame ONLY as the fallback, when the dedicated view fd is not active.
	if s.viewOut == nil {
		b.size += BufCameraStride + BufOverlayStride + BufSceneStride
	}

	return b
}

// newEdgeFrameBuild computes all counts and the trailing edge-label string section once per
// buildEdgeFrame call, plus the total (post-header) frame payload size. The Edge block-writer
// helper reads from the returned struct only — it never recomputes this data. Mirrors
// newSnapshotBuild's role, split for the Edge frame.
func (s *SnapshotState) newEdgeFrameBuild() *edgeFrameBuild {
	b := &edgeFrameBuild{
		edgeCount: uint32(len(s.edges)),
	}

	b.edgeLabelBytes, b.edgeLabelOffs, b.edgeLabelLens = s.buildEdgeLabelSection()
	b.edgeLabelBytesCount = len(b.edgeLabelBytes)

	b.size = int(b.edgeCount)*BufEdgeStride + b.edgeLabelBytesCount

	return b
}

// newNodeFrameBuild computes all counts and the trailing string sections once per
// buildNodeFrame call, plus the total (post-header) frame payload size. The Node/Interior/
// Port block-writer helpers read from the returned struct only — none of them recompute
// this data. Mirrors newSnapshotBuild's role, split for the node-owner-group frame.
func (s *SnapshotState) newNodeFrameBuild() *nodeFrameBuild {
	b := &nodeFrameBuild{
		nodeCount: uint32(len(s.nodes)),
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

	b.size = int(b.nodeCount)*BufNodeStride +
		b.interiorCount*BufInteriorStride +
		b.portCount*BufPortStride +
		b.labelBytesCount +
		b.portNameBytesCount

	return b
}

// writeHeader writes the fixed BufHeaderSize-byte SCENE header (no beadCount, no
// nodeCount/portCount/labelBytesCount/portNameBytesCount, no edgeCount/
// edgeLabelBytesCount — beads, the node-owner-group blocks, and the Edge block are their
// own tagged frames, see buildBeadFrame/buildNodeFrame/buildEdgeFrame) and increments
// s.tick. Returns the offset after the header.
func (s *SnapshotState) writeHeader(buf []byte, b *snapshotBuild) int {
	// Header: [tick][layoutLinkCount]. No eventCount: the EVENT block was RETIRED from
	// this frame (memory/feedback_no_single_writer_bridge.md — each emitting goroutine
	// now packs its OWN events into its OWN frame's trailing EVENTS section; see
	// stream_events.go / buildViewFrame's filtered fallback bucket for the kinds not yet
	// decentralized to a per-goroutine owner).
	off := 0
	binary.LittleEndian.PutUint32(buf[off:], s.tick)
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], b.layoutLinkCount)
	off += 4
	s.tick++
	return off
}

// buildEdgeFrame packs the Edge block + edge-label bytes into their own self-contained
// frame payload: BufEdgeFrameHeaderSize bytes
// ([tick:u32][edgeCount:u32][edgeLabelBytesCount:u32]) followed by the Edge block and the
// Edge-label bytes section — same row layout/order the writer helpers used to write
// inline into the scene frame (see frame_tags.go's BufBlockTagEdge comment for the full
// byte-layout doc). Does not touch s.tick: the scene frame's writeHeader is the sole tick
// incrementer (see buildBeadFrame's comment for why using the pre-increment value here is
// fine).
func (s *SnapshotState) buildEdgeFrame() []byte {
	b := s.newEdgeFrameBuild()
	buf := make([]byte, BufEdgeFrameHeaderSize+b.size)

	off := 0
	binary.LittleEndian.PutUint32(buf[off:], s.tick)
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], b.edgeCount)
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], uint32(b.edgeLabelBytesCount))
	off += 4

	off = s.writeEdgeBlock(buf, off, b)
	s.writeEdgeLabelBytesSection(buf, off, b)

	return buf
}

// buildNodeFrame packs the Node/Interior/Port blocks + Label/PortName bytes into their own
// self-contained frame payload: BufNodeFrameHeaderSize bytes
// ([tick:u32][nodeCount:u32][portCount:u32][labelBytesCount:u32][portNameBytesCount:u32])
// followed by the Node block, the Interior block (fixed BufInteriorSlotsPerNode rows per
// node — no separate count, derived from nodeCount), the Port block, the Label bytes
// section, and the Port-name bytes section — same row layout/order the writer helpers used
// to write inline into the scene frame (see frame_tags.go's BufBlockTagNode comment for the
// full byte-layout doc). These three blocks share one owner group (the node movers), so
// they travel together. Does not touch s.tick: the scene frame's writeHeader is the sole
// tick incrementer (see buildBeadFrame's comment for why using the pre-increment value here
// is fine).
func (s *SnapshotState) buildNodeFrame() []byte {
	b := s.newNodeFrameBuild()
	buf := make([]byte, BufNodeFrameHeaderSize+b.size)

	off := 0
	binary.LittleEndian.PutUint32(buf[off:], s.tick)
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], b.nodeCount)
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], uint32(b.portCount))
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], uint32(b.labelBytesCount))
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], uint32(b.portNameBytesCount))
	off += 4

	off = s.writeNodeBlock(buf, off, b)
	off = s.writeInteriorBlock(buf, off, b)
	off = s.writePortBlock(buf, off, b)
	off = s.writeLabelBytesSection(buf, off, b)
	s.writePortNameBytesSection(buf, off, b)

	return buf
}

// buildBeadFrame packs the current live-bead state into its own self-contained frame
// payload: [tick:u32][beadCount:u32] (BufBeadHeaderSize bytes) followed by beadCount ×
// BufBeadStride bead rows (same row layout writeBeadBlock used to write inline into the
// scene frame — see frame_tags.go for the full byte-layout doc). Does not touch s.tick:
// the scene frame's writeHeader is the sole tick incrementer; this uses whatever tick
// value is current at call time, giving both frames emitted by one emitSnapshot call
// the same tick number when called in sequence before the increment, or ticks that
// stay in lockstep with the scene stream either way (a monotonic counter, not a strict
// pairing key).
func (s *SnapshotState) buildBeadFrame() []byte {
	beadCount := len(s.beads)
	buf := make([]byte, BufBeadHeaderSize+beadCount*BufBeadStride)
	binary.LittleEndian.PutUint32(buf[0:], s.tick)
	binary.LittleEndian.PutUint32(buf[4:], uint32(beadCount))
	beadBuf := buf[BufBeadHeaderSize:]
	row := 0
	for _, bead := range s.beads {
		SetBeadRow(beadBuf, row,
			float32(bead.x), float32(bead.y), float32(bead.z),
			int32(bead.value), 1)
		row++
	}
	return buf
}

// writeNodeBlock writes the Node block: stable row order (insertion order of node IDs).
func (s *SnapshotState) writeNodeBlock(buf []byte, off int, b *nodeFrameBuild) int {
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
func (s *SnapshotState) writeInteriorBlock(buf []byte, off int, b *nodeFrameBuild) int {
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
// SrcPortRow/DstPortRow reference the Port block rows (NODE frame) that OWN the source
// (output) and dest (input) port world position — resolved here via portRowLookup (the
// same (node,port,isInput) -> row map the EVENT block uses) rather than storing a copy of
// the endpoint coordinate, so there is nothing on this row that can go stale under a drag
// (see bufLayoutEdge's doc comment in layout.go). -1 when a port isn't (yet) resolvable
// (e.g. a startup ordering gap) — the renderer treats this like any other unresolved row.
func (s *SnapshotState) writeEdgeBlock(buf []byte, off int, b *edgeFrameBuild) int {
	edgeBuf := buf[off : off+int(b.edgeCount)*BufEdgeStride]
	portRows := s.portRowLookup()
	for i, e := range s.edges {
		srcPortRow := int32(-1)
		if pr, ok := portRows[portLookupKey{e.srcNode, e.srcPort, false}]; ok {
			srcPortRow = int32(pr)
		}
		dstPortRow := int32(-1)
		if pr, ok := portRows[portLookupKey{e.dstNode, e.dstPort, true}]; ok {
			dstPortRow = int32(pr)
		}
		SetEdgeRow(edgeBuf, i,
			srcPortRow, dstPortRow, e.selected,
			b.edgeLabelOffs[i], b.edgeLabelLens[i])
	}
	return off + int(b.edgeCount)*BufEdgeStride
}

// edgeRowForPair returns the buffer edge-row index of the bead edge connecting node ids a/b
// (in either direction), or -1 when no such edge exists. Recomputed fresh every buildSnapshot
// call (not cached at load) so a layout link's overlay segment stays resolved to whichever
// edge row currently connects that pair — the Edge block's SrcPortRow/DstPortRow are
// themselves re-resolved every build, so riding along on the row index (rather than
// duplicating endpoints here) is what keeps the overlay attached under a drag.
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
func (s *SnapshotState) writePortBlock(buf []byte, off int, b *nodeFrameBuild) int {
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
func (s *SnapshotState) writeLabelBytesSection(buf []byte, off int, b *nodeFrameBuild) int {
	copy(buf[off:off+b.labelBytesCount], b.labelBytes)
	return off + b.labelBytesCount
}

// writePortNameBytesSection writes the Port-name bytes section (self-sizing via header
// portNameBytesCount): every port's name UTF-8 bytes in flattened port-row order;
// PortNameOff/PortNameLen slice into it.
func (s *SnapshotState) writePortNameBytesSection(buf []byte, off int, b *nodeFrameBuild) int {
	copy(buf[off:off+b.portNameBytesCount], b.portNameBytes)
	return off + b.portNameBytesCount
}

// writeEdgeLabelBytesSection writes the Edge-label bytes section (self-sizing via header
// edgeLabelBytesCount): every edge's label UTF-8 bytes in edge-row order;
// EdgeLabelOff/EdgeLabelLen slice into it.
func (s *SnapshotState) writeEdgeLabelBytesSection(buf []byte, off int, b *edgeFrameBuild) int {
	copy(buf[off:off+b.edgeLabelBytesCount], b.edgeLabelBytes)
	return off + b.edgeLabelBytesCount
}

// buildSnapshot packs all current state into one snapshot []byte. It is a short orchestrator:
// newSnapshotBuild computes all counts/string-sections once, then each block is written
// in the exact byte-layout order the header/format comment at the top of this file documents.
//
// Either/or with buildViewFrame (dual path, see snapshot.go's viewOut doc comment and
// Buffer/stream_fds.go): when s.viewOut is nil (no dedicated view fd — the fallback),
// camera/overlay/scene are embedded here, exactly as before this migration. When
// s.viewOut is non-nil (the dedicated fd is active), they are EXCLUDED here — they are
// written instead as their own frame on their own fd (buildViewFrame, called from
// emitSnapshot) — never double-sourced from both places at once.
func (s *SnapshotState) buildSnapshot() []byte {
	b := s.newSnapshotBuild()
	buf := make([]byte, b.size)

	off := s.writeHeader(buf, b)
	off = s.writeLayoutLinkBlock(buf, off, b)
	if s.viewOut == nil {
		off = s.writeCameraBlock(buf, off)
		off = s.writeOverlayBlock(buf, off)
		s.writeSceneBlock(buf, off)
	}

	return buf
}

// buildViewFrame packs the VIEW stream's own frame payload: BufViewFrameHeaderSize (4)
// bytes ([tick:u32]) followed by the Camera, Overlay, and Scene blocks — the SAME
// block-writer helpers buildSnapshot uses in its fallback branch, so there is exactly
// one place each block's bytes are produced regardless of which path is active. Called
// only when s.viewOut != nil (see buildSnapshot's either/or doc comment). Does not touch
// s.tick: buildSnapshot's writeHeader (called earlier in the same emitSnapshot) is the
// sole tick incrementer, mirroring buildBeadFrame/buildNodeFrame/buildEdgeFrame's
// existing "read the pre-increment tick" convention.
func (s *SnapshotState) buildViewFrame() []byte {
	buf := make([]byte, BufViewFrameHeaderSize+BufCameraStride+BufOverlayStride+BufSceneStride)
	binary.LittleEndian.PutUint32(buf[0:], s.tick)
	off := BufViewFrameHeaderSize
	off = s.writeCameraBlock(buf, off)
	off = s.writeOverlayBlock(buf, off)
	s.writeSceneBlock(buf, off)
	return append(buf, s.viewEventsSection()...)
}

// decentralizedEventKinds are the trace kinds that now ride their OWN emitting
// goroutine's per-owner frame (nodeMover/edgeMover/interiorStream — see owner_events.go,
// stream_events.go) instead of this pipeline. viewEventsSection excludes them so an
// event never appears twice in the .probe logs (once via its own owner fd, once here).
//
// NodeGeometry/Geometry are now fully decentralized: nodeMover.run/edgeMover.run each
// emit their own geometry ONCE at their own goroutine's start (before entering their
// loop), in addition to their existing per-move re-emit (nodeMover.emitGeometry/
// edgeMover.recomputeGeometry) — so the ONE-TIME load-time occurrence and every LIVE
// occurrence both resolve on the correct owner goroutine, with no central
// accumulator involved. The old node-Update-loop startup path (builders.go's injected
// EmitGeometry closure) that used to ALSO emit both kinds once per node/edge is now
// left uninjected (see injectClosures' doc comment) — it would have double-counted
// against this per-owner emit for the identical, redundant values.
var decentralizedEventKinds = map[string]bool{
	T.KindPosition:     true,
	T.KindArrive:       true,
	T.KindNodeBead:     true,
	T.KindNodeGeometry: true,
	T.KindGeometry:     true,
}

// viewEventsSection packs the VIEW frame's trailing EVENTS section: every buffered event
// whose kind is NOT already decentralized to its own owner fd (decentralizedEventKinds).
// This is the FALLBACK bucket for kinds not yet migrated to a per-goroutine owner
// (Fire/Recv/Send/Select/Hover/AbcDrag*/Camera/SceneSphere/overlay toggles/LayoutLink) —
// they still flow through the existing Trace-drain → SnapshotState.pendingEvents pipeline,
// which is also what already produces this exact VIEW frame's Camera/Overlay/Scene block
// values, so packing them here reuses an existing single-writer pipeline rather than
// introducing a new one. Real per-goroutine decentralization for THESE kinds is future work.
func (s *SnapshotState) viewEventsSection() []byte {
	if len(s.pendingEvents) == 0 {
		return BuildEventsSection(nil)
	}
	portRows := s.portRowLookup()
	kept := make([]StreamEvent, 0, len(s.pendingEvents))
	for _, e := range s.pendingEvents {
		if decentralizedEventKinds[e.kind] {
			continue
		}
		nodeRow := int32(s.nodeRowIndex(e.node))
		portRow := int32(-1)
		if e.port != "" {
			if pr, ok := portRows[portLookupKey{e.node, e.port, e.portIsInput}]; ok {
				portRow = int32(pr)
			}
		}
		targetRow := int32(-1)
		if e.target != "" {
			targetRow = int32(s.nodeRowIndex(e.target))
		}
		targetPortRow := int32(-1)
		if e.targetHandle != "" {
			if pr, ok := portRows[portLookupKey{e.target, e.targetHandle, true}]; ok {
				targetPortRow = int32(pr)
			}
		}
		edgeRow := int32(-1)
		if e.edge != "" {
			if idx, ok := s.edgeIndex[e.edge]; ok {
				edgeRow = int32(idx)
			}
		}
		kept = append(kept, StreamEvent{
			Kind: s.kindID[e.kind], NodeRow: nodeRow, PortRow: portRow,
			TargetRow: targetRow, TargetPortRow: targetPortRow, EdgeRow: edgeRow,
			Slot: int32(e.slot), Value: int32(e.value), Bead: uint32(e.bead),
			ArcLength: float32(e.arc), SimLatencyMs: float32(e.lat),
			X: float32(e.x), Y: float32(e.y), Z: float32(e.z), F: float32(e.f),
		})
	}
	return BuildEventsSection(kept)
}
