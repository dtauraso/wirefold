// Buffer/edge_stream_frame.go — the per-edge dedicated-stream frame packer (see
// Buffer/stream_fds.go's StreamKindEdge doc comment and
// memory/feedback_no_single_writer_bridge.md). This is the combined frame ONE edgeMover
// goroutine writes to ITS OWN fd every cycle it changes (geometry recompute OR a bead
// step) — the edge's own Edge-block row PLUS the beads currently in flight on that
// edge's wire, with no sub-tag byte (the fd position already identifies which edge).
//
// Wire bytes (no outer tag, unlike fd3's [len][tag][payload] — a dedicated fd's OWN
// position already identifies the stream, so there is nothing left to discriminate):
//
//	[tick:u32]
//	Edge     BufEdgeStride bytes (SrcPortRow/DstPortRow/Selected + EdgeLabelOff=0/Len,
//	         written via the SAME SetEdgeRow column writer buildEdgeFrame uses)
//	EdgeLabel labelLen bytes (this edge's own label bytes — inline, not a shared section:
//	         each edge's own stream carries its own label bytes)
//	[beadCount:u32]
//	Bead     beadCount × BufBeadStride bytes (SAME SetBeadRow column writer buildBeadFrame
//	         uses), this edge's wire's own live in-flight beads only
//
// Injected into nodes/Wiring's MoveDispatch.SetEdgeStreams as a plain func (not a Buffer
// import in the Wiring package — mirrors PortRowResolver/EdgeRowResolver's existing
// interface-injection pattern, keeping Wiring Buffer-independent).
package Buffer

import "encoding/binary"

// BuildEdgeStreamFrame packs one edge's combined per-fd frame payload (see this file's
// header comment for the byte layout). beadVal/beadX/beadY/beadZ are parallel slices
// (same length, same order) describing this edge's wire's current live in-flight beads —
// supplied by the caller (edgeMover, via PacedWire.LiveBeadRows) so this package needs no
// dependency on nodes/Wiring's bead type.
func BuildEdgeStreamFrame(tick uint32, srcPortRow, dstPortRow int32, selected uint8, label string, beadVal []int32, beadX, beadY, beadZ []float32, events []StreamEvent) []byte {
	labelBytes := []byte(label)
	beadCount := len(beadVal)
	size := 4 + BufEdgeStride + len(labelBytes) + 4 + beadCount*BufBeadStride
	buf := make([]byte, size)
	off := 0
	binary.LittleEndian.PutUint32(buf[off:], tick)
	off += 4
	// edgeLabelOff=0: this frame's own label bytes immediately follow the Edge row —
	// there is no shared EdgeLabel section on a dedicated per-edge stream.
	SetEdgeRow(buf[off:off+BufEdgeStride], 0, srcPortRow, dstPortRow, selected, 0, uint32(len(labelBytes)))
	off += BufEdgeStride
	copy(buf[off:off+len(labelBytes)], labelBytes)
	off += len(labelBytes)
	binary.LittleEndian.PutUint32(buf[off:], uint32(beadCount))
	off += 4
	beadBuf := buf[off:]
	for i := 0; i < beadCount; i++ {
		SetBeadRow(beadBuf, i, beadX[i], beadY[i], beadZ[i], beadVal[i], 1)
	}
	return append(buf, BuildEventsSection(events)...)
}
