// buffer-decode.ts — pure snapshot decoder.
//
// decodeSnapshot takes an ArrayBuffer produced by Go's Buffer.SnapshotState and
// returns DataView slices over each column block — zero-copy, no store writes.
//
// Layout (little-endian, packed):
//   Header   40 bytes : [tick][beadCount][nodeCount][edgeCount][portCount][labelBytesCount][eventCount][portNameBytesCount][edgeLabelBytesCount][layoutLinkCount] (u32 each)
//   Bead     beadCount × BEAD_STRIDE bytes
//   Node     nodeCount × NODE_STRIDE bytes
//   Interior nodeCount × INTERIOR_SLOTS_PER_NODE × INTERIOR_STRIDE bytes
//   Edge     edgeCount × EDGE_STRIDE bytes
//   LayoutLink layoutLinkCount × LAYOUT_LINK_STRIDE bytes (the LAYOUT double-link overlay
//              pairs, from LocalPolars — NOT the Edge block)
//   Port     portCount × PORT_STRIDE bytes   (flattened over nodes in node-row order)
//   Camera   CAMERA_STRIDE bytes   (always 1 row)
//   Overlay  OVERLAY_STRIDE bytes  (always 1 row)
//   Scene    SCENE_STRIDE bytes    (always 1 row; persisted scene-sphere center+radius)
//   Label    labelBytesCount bytes (node labels' UTF-8 bytes, node-row order)
//   Event    eventCount × EVENT_STRIDE bytes (per-tick causal trace events; .probe log only)
//   PortName portNameBytesCount bytes (port names' UTF-8 bytes, flattened port-row order)
//   EdgeLabel edgeLabelBytesCount bytes (edge labels' UTF-8 bytes, edge-row order)

import {
  BUF_HEADER_SIZE,
  BEAD_STRIDE,
  NODE_STRIDE,
  INTERIOR_STRIDE,
  INTERIOR_SLOTS_PER_NODE,
  EDGE_STRIDE,
  LAYOUT_LINK_STRIDE,
  PORT_STRIDE,
  CAMERA_STRIDE,
  OVERLAY_STRIDE,
  SCENE_STRIDE,
  EVENT_STRIDE,
  readNodeLabelOff,
  readNodeLabelLen,
  readPortPortNameOff,
  readPortPortNameLen,
  readEdgeEdgeLabelOff,
  readEdgeEdgeLabelLen,
} from "../../schema/buffer-layout";
// Generated (part of BUF_LAYOUT_FINGERPRINT) — re-exported here so existing consumers
// (buffer-scene.tsx, InteriorBeadInstances.tsx, buffer-log.ts) keep importing it from the
// decode module rather than reaching into schema/buffer-layout directly.
export { INTERIOR_SLOTS_PER_NODE } from "../../schema/buffer-layout";

/** Shared UTF-8 decoder for the label / port-name / edge-label sections. */
const STR_DECODER = new TextDecoder();

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
  /** Number of LAYOUT-link pairs (from LocalPolars, NOT the Edge block). */
  layoutLinkCount: number;
  /** DataView over the LayoutLink block; byteLength = layoutLinkCount × LAYOUT_LINK_STRIDE. */
  layoutLinkView: DataView;
  /** DataView over the port block only; byteLength = portCount × PORT_STRIDE. Row i is the
   *  buffer port row i — the same index a port InstancedMesh instanceId carries for picking. */
  portView: DataView;
  /** DataView over the single camera row. */
  cameraView: DataView;
  /** DataView over the single overlay row. */
  overlayView: DataView;
  /** DataView over the single scene-sphere row (persisted center + radius; established once
   *  at load and never moved — see readSceneCX/readSceneRadius, KindSceneSphere). */
  sceneView: DataView;
  /** Total bytes in the trailing label section (self-sizing via the header labelBytesCount). */
  labelBytesCount: number;
  /** Uint8 view over the label-bytes section: every node's label UTF-8 bytes concatenated in
   *  node-row order. A node's label is labelBytes[LabelOff : LabelOff+LabelLen) — see nodeLabel. */
  labelBytes: Uint8Array;
  /** Number of per-tick causal events in this snapshot's EVENT block (.probe log only). */
  eventCount: number;
  /** DataView over the EVENT block; byteLength = eventCount × EVENT_STRIDE. */
  eventView: DataView;
  /** Uint8 view over the port-name-bytes section (flattened port-row order). See portName. */
  portNameBytes: Uint8Array;
  /** Uint8 view over the edge-label-bytes section (edge-row order). See edgeLabel. */
  edgeLabelBytes: Uint8Array;
}

// Single-entry memo keyed on the ArrayBuffer's OBJECT IDENTITY (not its contents — the
// buffer's bytes never mutate in place, a new ArrayBuffer arrives per snapshot). Every
// per-block renderer (BeadInstances, NodeInstances, PortInstances, EdgeTube, SphereRings,
// SelectionHighlight ×2, BufferCamera, BufferLabelProjector, InteriorBeadInstances,
// NavGuides, ThreeView ×2 — ~14 call sites) independently decodes the SAME snapshot every
// frame; without this cache each one builds its own ~10 DataViews, ~140 short-lived
// DataViews/frame at 60fps under a ~430-700 snapshot/sec stream. This shares one decode
// per frame across all consumers. It moves no ownership — the memo just skips redoing
// pure arithmetic on unchanged input, exactly what memoization is for.
let lastBuf: ArrayBuffer | null = null;
let lastDecoded: DecodedSnapshot | null = null;

/**
 * Decode a snapshot ArrayBuffer into typed block views.
 *
 * Returns null if the buffer is too small to be a valid snapshot
 * (guards against partial frames or empty buffers).
 *
 * This is a PURE function — no side effects, no store reads/writes.
 * All views alias the original buffer (zero-copy). Memoized on `buf`'s identity (see
 * lastBuf/lastDecoded above) so N consumers decoding the same snapshot in one frame share
 * a single decode.
 */
export function decodeSnapshot(buf: ArrayBuffer): DecodedSnapshot | null {
  if (buf === lastBuf) return lastDecoded;
  const decoded = decodeSnapshotUncached(buf);
  lastBuf = buf;
  lastDecoded = decoded;
  return decoded;
}

function decodeSnapshotUncached(buf: ArrayBuffer): DecodedSnapshot | null {
  if (buf.byteLength < BUF_HEADER_SIZE) return null;

  const hdr = new DataView(buf, 0, BUF_HEADER_SIZE);
  const tick                = hdr.getUint32(0,  true);
  const beadCount           = hdr.getUint32(4,  true);
  const nodeCount           = hdr.getUint32(8,  true);
  const edgeCount           = hdr.getUint32(12, true);
  const portCount           = hdr.getUint32(16, true);
  const labelBytesCount     = hdr.getUint32(20, true);
  const eventCount          = hdr.getUint32(24, true);
  const portNameBytesCount  = hdr.getUint32(28, true);
  const edgeLabelBytesCount = hdr.getUint32(32, true);
  const layoutLinkCount     = hdr.getUint32(36, true);

  const interiorCount = nodeCount * INTERIOR_SLOTS_PER_NODE;

  const beadBytes       = beadCount * BEAD_STRIDE;
  const nodeBytes       = nodeCount * NODE_STRIDE;
  const interiorBytes   = interiorCount * INTERIOR_STRIDE;
  const edgeBytes       = edgeCount * EDGE_STRIDE;
  const layoutLinkBytes = layoutLinkCount * LAYOUT_LINK_STRIDE;
  const portBytes       = portCount * PORT_STRIDE;
  const eventBytes      = eventCount * EVENT_STRIDE;
  const expectedLen = BUF_HEADER_SIZE + beadBytes + nodeBytes + interiorBytes + edgeBytes +
                      layoutLinkBytes + portBytes + CAMERA_STRIDE + OVERLAY_STRIDE + SCENE_STRIDE +
                      labelBytesCount + eventBytes + portNameBytesCount + edgeLabelBytesCount;

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

  const layoutLinkView = new DataView(buf, off, layoutLinkBytes);
  off += layoutLinkBytes;

  const portView = new DataView(buf, off, portBytes);
  off += portBytes;

  const cameraView = new DataView(buf, off, CAMERA_STRIDE);
  off += CAMERA_STRIDE;

  const overlayView = new DataView(buf, off, OVERLAY_STRIDE);
  off += OVERLAY_STRIDE;

  const sceneView = new DataView(buf, off, SCENE_STRIDE);
  off += SCENE_STRIDE;

  const labelBytes = new Uint8Array(buf, off, labelBytesCount);
  off += labelBytesCount;

  const eventView = new DataView(buf, off, eventBytes);
  off += eventBytes;

  const portNameBytes = new Uint8Array(buf, off, portNameBytesCount);
  off += portNameBytesCount;

  const edgeLabelBytes = new Uint8Array(buf, off, edgeLabelBytesCount);

  return {
    tick, beadCount, nodeCount, edgeCount, portCount, beadView, nodeView, interiorCount,
    interiorView, edgeView, layoutLinkCount, layoutLinkView, portView, cameraView, overlayView,
    sceneView, labelBytesCount, labelBytes, eventCount, eventView, portNameBytes, edgeLabelBytes,
  };
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
  if (off < 0 || len < 0 || off + len > decoded.labelBytes.byteLength) return "";
  return STR_DECODER.decode(decoded.labelBytes.subarray(off, off + len));
}

/**
 * Port name for buffer port row `row`: slice out of the decoded port-name-bytes section.
 * Returns "" when the port has no name. Used only by the buffer-decoded .probe logger — the
 * render/bridge path resolves a port hit by row index, never by this string.
 */
export function portName(decoded: DecodedSnapshot, row: number): string {
  if (row < 0) return "";
  const off = readPortPortNameOff(decoded.portView, row);
  const len = readPortPortNameLen(decoded.portView, row);
  if (len === 0) return "";
  if (off < 0 || len < 0 || off + len > decoded.portNameBytes.byteLength) return "";
  return STR_DECODER.decode(decoded.portNameBytes.subarray(off, off + len));
}

/**
 * Edge label for buffer edge row `row`: slice out of the decoded edge-label-bytes section.
 * Returns "" when the edge has no label. Used only by the buffer-decoded .probe logger — the
 * render/bridge path resolves an edge hit by row index, never by this string.
 */
export function edgeLabel(decoded: DecodedSnapshot, row: number): string {
  if (row < 0) return "";
  const off = readEdgeEdgeLabelOff(decoded.edgeView, row);
  const len = readEdgeEdgeLabelLen(decoded.edgeView, row);
  if (len === 0) return "";
  if (off < 0 || len < 0 || off + len > decoded.edgeLabelBytes.byteLength) return "";
  return STR_DECODER.decode(decoded.edgeLabelBytes.subarray(off, off + len));
}
