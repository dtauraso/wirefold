// buffer-decode.ts — pure snapshot decoder.
//
// decodeSnapshot takes an ArrayBuffer produced by Go's Buffer.SnapshotState and
// returns DataView slices over each column block — zero-copy, no store writes.
//
// Layout (little-endian, packed):
//   Header  16 bytes : [tick:u32][beadCount:u32][nodeCount:u32][edgeCount:u32]
//   Bead    beadCount × BEAD_STRIDE bytes
//   Node    nodeCount × NODE_STRIDE bytes
//   Edge    edgeCount × EDGE_STRIDE bytes
//   Camera  CAMERA_STRIDE bytes   (always 1 row)
//   Overlay OVERLAY_STRIDE bytes  (always 1 row)

import {
  BUF_HEADER_SIZE,
  BEAD_STRIDE,
  NODE_STRIDE,
  EDGE_STRIDE,
  CAMERA_STRIDE,
  OVERLAY_STRIDE,
} from "../../schema/buffer-layout";

export interface DecodedSnapshot {
  tick: number;
  beadCount: number;
  nodeCount: number;
  edgeCount: number;
  /** DataView over the bead block only; byteLength = beadCount × BEAD_STRIDE. */
  beadView: DataView;
  /** DataView over the node block only; byteLength = nodeCount × NODE_STRIDE. */
  nodeView: DataView;
  /** DataView over the edge block only; byteLength = edgeCount × EDGE_STRIDE. */
  edgeView: DataView;
  /** DataView over the single camera row. */
  cameraView: DataView;
  /** DataView over the single overlay row. */
  overlayView: DataView;
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
  const tick       = hdr.getUint32(0,  true);
  const beadCount  = hdr.getUint32(4,  true);
  const nodeCount  = hdr.getUint32(8,  true);
  const edgeCount  = hdr.getUint32(12, true);

  const beadBytes   = beadCount * BEAD_STRIDE;
  const nodeBytes   = nodeCount * NODE_STRIDE;
  const edgeBytes   = edgeCount * EDGE_STRIDE;
  const expectedLen = BUF_HEADER_SIZE + beadBytes + nodeBytes + edgeBytes +
                      CAMERA_STRIDE + OVERLAY_STRIDE;

  if (buf.byteLength < expectedLen) return null;

  let off = BUF_HEADER_SIZE;

  const beadView = new DataView(buf, off, beadBytes);
  off += beadBytes;

  const nodeView = new DataView(buf, off, nodeBytes);
  off += nodeBytes;

  const edgeView = new DataView(buf, off, edgeBytes);
  off += edgeBytes;

  const cameraView = new DataView(buf, off, CAMERA_STRIDE);
  off += CAMERA_STRIDE;

  const overlayView = new DataView(buf, off, OVERLAY_STRIDE);

  return { tick, beadCount, nodeCount, edgeCount, beadView, nodeView, edgeView, cameraView, overlayView };
}
