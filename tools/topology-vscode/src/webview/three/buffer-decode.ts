// buffer-decode.ts — pure snapshot decoder.
//
// decodeSnapshot takes an ArrayBuffer produced by Go's Buffer.SnapshotState and
// returns DataView slices over each column block — zero-copy, no store writes.
//
// Layout (little-endian, packed):
//   Header   24 bytes : [tick:u32][beadCount:u32][nodeCount:u32][edgeCount:u32][portCount:u32][labelBytesCount:u32]
//   Bead     beadCount × BEAD_STRIDE bytes
//   Node     nodeCount × NODE_STRIDE bytes
//   Interior nodeCount × INTERIOR_SLOTS_PER_NODE × INTERIOR_STRIDE bytes
//   Edge     edgeCount × EDGE_STRIDE bytes
//   Port     portCount × PORT_STRIDE bytes   (flattened over nodes in node-row order)
//   Camera   CAMERA_STRIDE bytes   (always 1 row)
//   Overlay  OVERLAY_STRIDE bytes  (always 1 row)
//   Label    labelBytesCount bytes (node labels' UTF-8 bytes, node-row order)

import {
  BUF_HEADER_SIZE,
  BEAD_STRIDE,
  NODE_STRIDE,
  INTERIOR_STRIDE,
  EDGE_STRIDE,
  PORT_STRIDE,
  CAMERA_STRIDE,
  OVERLAY_STRIDE,
  readNodeLabelOff,
  readNodeLabelLen,
} from "../../schema/buffer-layout";

/** Shared UTF-8 decoder for node labels (see nodeLabel). */
const LABEL_DECODER = new TextDecoder();

/**
 * Fixed interior grid slots per node in the Interior block. MUST match
 * BufInteriorSlotsPerNode in Buffer/layout.go — the Interior block carries exactly
 * nodeCount × INTERIOR_SLOTS_PER_NODE rows (slot = gridRow*2 + gridCol), so it has no
 * separate header count. Locked in parity by the interior-block decode test.
 */
export const INTERIOR_SLOTS_PER_NODE = 4;

export interface DecodedSnapshot {
  tick: number;
  beadCount: number;
  nodeCount: number;
  edgeCount: number;
  /** Total port rows across all nodes (self-sizing via the header portCount field). */
  portCount: number;
  /** DataView over the bead block only; byteLength = beadCount × BEAD_STRIDE. */
  beadView: DataView;
  /** DataView over the node block only; byteLength = nodeCount × NODE_STRIDE. */
  nodeView: DataView;
  /** Interior grid rows (nodeCount × INTERIOR_SLOTS_PER_NODE); row = nodeRow*slots + slot. */
  interiorCount: number;
  /** DataView over the interior block; byteLength = interiorCount × INTERIOR_STRIDE. */
  interiorView: DataView;
  /** DataView over the edge block only; byteLength = edgeCount × EDGE_STRIDE. */
  edgeView: DataView;
  /** DataView over the port block only; byteLength = portCount × PORT_STRIDE. Row i is the
   *  buffer port row i — the same index a port InstancedMesh instanceId carries for picking. */
  portView: DataView;
  /** DataView over the single camera row. */
  cameraView: DataView;
  /** DataView over the single overlay row. */
  overlayView: DataView;
  /** Total bytes in the trailing label section (self-sizing via the header labelBytesCount). */
  labelBytesCount: number;
  /** Uint8 view over the label-bytes section: every node's label UTF-8 bytes concatenated in
   *  node-row order. A node's label is labelBytes[LabelOff : LabelOff+LabelLen) — see nodeLabel. */
  labelBytes: Uint8Array;
}

/**
 * Decode a snapshot ArrayBuffer into typed block views.
 *
 * Returns null if the buffer is too small to be a valid snapshot
 * (guards against partial frames or empty buffers).
 *
 * This is a PURE function — no side effects, no store reads/writes.
 * All views alias the original buffer (zero-copy).
 */
export function decodeSnapshot(buf: ArrayBuffer): DecodedSnapshot | null {
  if (buf.byteLength < BUF_HEADER_SIZE) return null;

  const hdr = new DataView(buf, 0, BUF_HEADER_SIZE);
  const tick            = hdr.getUint32(0,  true);
  const beadCount       = hdr.getUint32(4,  true);
  const nodeCount       = hdr.getUint32(8,  true);
  const edgeCount       = hdr.getUint32(12, true);
  const portCount       = hdr.getUint32(16, true);
  const labelBytesCount = hdr.getUint32(20, true);

  const interiorCount = nodeCount * INTERIOR_SLOTS_PER_NODE;

  const beadBytes     = beadCount * BEAD_STRIDE;
  const nodeBytes     = nodeCount * NODE_STRIDE;
  const interiorBytes = interiorCount * INTERIOR_STRIDE;
  const edgeBytes     = edgeCount * EDGE_STRIDE;
  const portBytes     = portCount * PORT_STRIDE;
  const expectedLen = BUF_HEADER_SIZE + beadBytes + nodeBytes + interiorBytes + edgeBytes +
                      portBytes + CAMERA_STRIDE + OVERLAY_STRIDE + labelBytesCount;

  if (buf.byteLength < expectedLen) return null;

  let off = BUF_HEADER_SIZE;

  const beadView = new DataView(buf, off, beadBytes);
  off += beadBytes;

  const nodeView = new DataView(buf, off, nodeBytes);
  off += nodeBytes;

  const interiorView = new DataView(buf, off, interiorBytes);
  off += interiorBytes;

  const edgeView = new DataView(buf, off, edgeBytes);
  off += edgeBytes;

  const portView = new DataView(buf, off, portBytes);
  off += portBytes;

  const cameraView = new DataView(buf, off, CAMERA_STRIDE);
  off += CAMERA_STRIDE;

  const overlayView = new DataView(buf, off, OVERLAY_STRIDE);
  off += OVERLAY_STRIDE;

  const labelBytes = new Uint8Array(buf, off, labelBytesCount);

  return { tick, beadCount, nodeCount, edgeCount, portCount, beadView, nodeView, interiorCount, interiorView, edgeView, portView, cameraView, overlayView, labelBytesCount, labelBytes };
}

/**
 * Human label for buffer node row `row`: slice [LabelOff, LabelOff+LabelLen) out of the
 * decoded label-bytes section and UTF-8 decode it. Returns "" when the node has no label
 * (LabelLen == 0). Pure — reads only the decoded snapshot. This is the row-keyed replacement
 * for the removed id/label sidecar: the label rides the binary buffer.
 */
export function nodeLabel(decoded: DecodedSnapshot, row: number): string {
  const off = readNodeLabelOff(decoded.nodeView, row);
  const len = readNodeLabelLen(decoded.nodeView, row);
  if (len === 0) return "";
  return LABEL_DECODER.decode(decoded.labelBytes.subarray(off, off + len));
}
