// Buffer/node_stream_frame.go — the per-node dedicated-stream frame packers (see
// Buffer/stream_fds.go's StreamKindNode/StreamKindInterior doc comments and
// memory/feedback_no_single_writer_bridge.md). Two frames, one per emitting goroutine:
//
//   - BuildNodeStreamFrame is written by ONE node's nodeMover goroutine — its own Node row
//     (center/radius/ring-normals/selection-UI columns) + its own ports + its own inline
//     label bytes. No sub-tag byte (the fd position already identifies which node).
//   - BuildInteriorStreamFrame is written by ONE node's OWN Update goroutine (the second
//     emitting goroutine per node) — its own fixed 4-slot interior-bead grid.
//
// Both take plain parallel slices/scalars rather than any Buffer-side struct, mirroring
// BuildEdgeStreamFrame's shape: the emitting side lives in nodes/Wiring, which must stay
// Buffer-independent (see PortRowResolver/EdgeRowResolver's existing interface-injection
// pattern), so the injected build funcs it holds can only close over plain Go values.
package Buffer

import "encoding/binary"

// BuildNodeStreamFrame packs one node's combined per-fd frame payload (no outer tag byte
// — the fd position already identifies which node this is):
//
//	[tick:u32]
//	[portCount:u32]
//	[labelLen:u32]
//	[portNameBytesCount:u32]
//	Node     BufNodeStride bytes (SAME SetNodeRow column writer buildNodeFrame uses;
//	         LabelOff=0 into this frame's own label bytes, NodeRow-local — nodeRow is
//	         carried separately below for the Port rows' NodeRow column)
//	Label    labelLen bytes (this node's own label bytes — inline, not a shared section)
//	Port     portCount × BufPortStride bytes (SAME SetPortRow column writer buildNodeFrame
//	         uses; every row's NodeRow = nodeRow, PortNameOff/Len into this frame's own
//	         port-name bytes)
//	PortName portNameBytesCount bytes (this node's own ports' name bytes, concatenated in
//	         the same order as the Port rows above)
func BuildNodeStreamFrame(
	tick uint32, nodeRow int32,
	cx, cy, cz, radius, sphereR float32,
	vrx, vry, vrz, frx, fry, frz float32,
	selected, kindID, hovered, latchedSel, gotDragMsg uint8,
	dragDeltaA, dragDeltaB, dragDeltaC int32,
	label string,
	portNames []string,
	portDX, portDY, portDZ, portPX, portPY, portPZ []float32,
	portIsInput, portHovered []uint8,
) []byte {
	labelBytes := []byte(label)
	portCount := len(portNames)
	portNameBytes := make([]byte, 0, portCount*8)
	portNameOffs := make([]uint32, portCount)
	portNameLens := make([]uint32, portCount)
	for i, n := range portNames {
		portNameOffs[i] = uint32(len(portNameBytes))
		nb := []byte(n)
		portNameLens[i] = uint32(len(nb))
		portNameBytes = append(portNameBytes, nb...)
	}

	size := 16 + BufNodeStride + len(labelBytes) + portCount*BufPortStride + len(portNameBytes)
	buf := make([]byte, size)
	off := 0
	binary.LittleEndian.PutUint32(buf[off:], tick)
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], uint32(portCount))
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], uint32(len(labelBytes)))
	off += 4
	binary.LittleEndian.PutUint32(buf[off:], uint32(len(portNameBytes)))
	off += 4

	SetNodeRow(buf[off:off+BufNodeStride], 0, cx, cy, cz, radius, sphereR, vrx, vry, vrz, frx, fry, frz,
		selected, kindID, 0, uint32(len(labelBytes)), hovered, latchedSel, gotDragMsg,
		dragDeltaA, dragDeltaB, dragDeltaC)
	off += BufNodeStride

	copy(buf[off:off+len(labelBytes)], labelBytes)
	off += len(labelBytes)

	portBuf := buf[off : off+portCount*BufPortStride]
	for i := range portNames {
		SetPortRow(portBuf, i, nodeRow, portDX[i], portDY[i], portDZ[i], portPX[i], portPY[i], portPZ[i],
			portIsInput[i], portHovered[i], portNameOffs[i], portNameLens[i])
	}
	off += portCount * BufPortStride

	copy(buf[off:off+len(portNameBytes)], portNameBytes)
	return buf
}

// BuildInteriorStreamFrame packs one node's fixed-slot interior-bead frame payload (no
// outer tag byte): [tick:u32] followed by len(present) Interior rows (SAME SetInteriorRow
// column writer buildNodeFrame uses) — no count, the decoder derives the length from the
// fixed per-node slot count (BufInteriorSlotsPerNode), same as the shared fd-3 Interior
// block. present/value/ox/oy/oz are parallel slices, same length, same slot order.
func BuildInteriorStreamFrame(tick uint32, present []uint8, value []int32, ox, oy, oz []float32) []byte {
	n := len(present)
	buf := make([]byte, 4+n*BufInteriorStride)
	binary.LittleEndian.PutUint32(buf[0:], tick)
	interiorBuf := buf[4:]
	for i := 0; i < n; i++ {
		SetInteriorRow(interiorBuf, i, present[i], value[i], ox[i], oy[i], oz[i])
	}
	return buf
}
