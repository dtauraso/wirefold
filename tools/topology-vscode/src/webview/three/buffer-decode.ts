// buffer-decode.ts — pure snapshot + bead-frame + node-frame decoders.
//
// decodeSnapshot takes the SCENE-tagged ArrayBuffer produced by Go's Buffer.SnapshotState
// (BUF_BLOCK_TAG_SCENE) and returns DataView slices over each column block — zero-copy, no
// store writes. decodeBeadFrame takes the separate BEAD-tagged frame
// (BUF_BLOCK_TAG_BEAD, see frame-tags.ts) and returns just its bead rows — beads no
// longer ride the scene frame. decodeNodeFrame takes the separate NODE-tagged frame
// (BUF_BLOCK_TAG_NODE) and returns the Node/Interior/Port blocks + Label/PortName
// bytes — those three blocks share one owner group (the node movers), so they no
// longer ride the scene frame either. decodeEdgeFrame takes the separate EDGE-tagged
// frame (BUF_BLOCK_TAG_EDGE) and returns the Edge block + EdgeLabel bytes — the Edge
// block carries NO endpoint coordinates (it references its two port rows, resolved
// against the NODE frame's Port block — see EdgeTube.tsx), so it no longer rides the
// scene frame either.
//
// Scene layout (little-endian, packed):
//   Header   12 bytes : [tick][eventCount][layoutLinkCount] (u32 each)
//   LayoutLink layoutLinkCount × LAYOUT_LINK_STRIDE bytes (the LAYOUT double-link overlay
//              pairs, from LocalPolars — NOT the Edge block; SrcNodeRow/DstNodeRow resolve
//              against the NODE frame's Node block — see decodeNodeFrame)
//   Camera   CAMERA_STRIDE bytes   (always 1 row)
//   Overlay  OVERLAY_STRIDE bytes  (always 1 row)
//   Scene    SCENE_STRIDE bytes    (always 1 row; persisted scene-sphere center+radius)
//   Event    eventCount × EVENT_STRIDE bytes (per-tick causal trace events; .probe log only)
//
// Node frame layout (little-endian, packed; see frame-tags.ts BUF_BLOCK_TAG_NODE):
//   Header   BUF_NODE_FRAME_HEADER_SIZE (20) bytes: [tick][nodeCount][portCount]
//            [labelBytesCount][portNameBytesCount] (u32 each)
//   Node     nodeCount × NODE_STRIDE bytes
//   Interior nodeCount × INTERIOR_SLOTS_PER_NODE × INTERIOR_STRIDE bytes
//   Port     portCount × PORT_STRIDE bytes   (flattened over nodes in node-row order)
//   Label    labelBytesCount bytes (node labels' UTF-8 bytes, node-row order)
//   PortName portNameBytesCount bytes (port names' UTF-8 bytes, flattened port-row order)
//
// Edge frame layout (little-endian, packed; see frame-tags.ts BUF_BLOCK_TAG_EDGE):
//   Header    BUF_EDGE_FRAME_HEADER_SIZE (12) bytes: [tick][edgeCount][edgeLabelBytesCount] (u32 each)
//   Edge      edgeCount × EDGE_STRIDE bytes
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
import { BUF_BEAD_HEADER_SIZE, BUF_NODE_FRAME_HEADER_SIZE, BUF_EDGE_FRAME_HEADER_SIZE, BUF_VIEW_FRAME_HEADER_SIZE, BUF_EDGE_STREAM_FRAME_HEADER_SIZE } from "../../schema/frame-tags";
// Generated (part of BUF_LAYOUT_FINGERPRINT) — re-exported here so existing consumers
// (buffer-scene.tsx, InteriorBeadInstances.tsx, buffer-log.ts) keep importing it from the
// decode module rather than reaching into schema/buffer-layout directly.
export { INTERIOR_SLOTS_PER_NODE } from "../../schema/buffer-layout";

/** Shared UTF-8 decoder for the label / port-name / edge-label sections. */
const STR_DECODER = new TextDecoder();

export interface DecodedSnapshot {
  tick: number;
  /** Number of LAYOUT-link pairs (from LocalPolars, NOT the Edge block). */
  layoutLinkCount: number;
  /** DataView over the LayoutLink block; byteLength = layoutLinkCount × LAYOUT_LINK_STRIDE.
   *  SrcNodeRow/DstNodeRow resolve against the NODE frame's Node block (decodeNodeFrame) —
   *  both frames share the same stable node-row order. */
  layoutLinkView: DataView;
  /** DataView over the single camera row, or null when the dedicated VIEW fd is active
   *  (camera/overlay/scene then arrive on their own frame instead — see decodeViewFrame
   *  / three/view-blocks.ts's either/or read). Non-null in the fallback (no dedicated
   *  view fd), exactly as before this migration. */
  cameraView: DataView | null;
  /** DataView over the single overlay row, or null — see cameraView's doc comment. */
  overlayView: DataView | null;
  /** DataView over the single scene-sphere row (persisted center + radius; established once
   *  at load and never moved — see readSceneCX/readSceneRadius, KindSceneSphere), or null —
   *  see cameraView's doc comment. */
  sceneView: DataView | null;
  /** Number of per-tick causal events in this snapshot's EVENT block (.probe log only). */
  eventCount: number;
  /** DataView over the EVENT block; byteLength = eventCount × EVENT_STRIDE. */
  eventView: DataView;
}

// Single-entry memo keyed on the ArrayBuffer's OBJECT IDENTITY (not its contents — the
// buffer's bytes never mutate in place, a new ArrayBuffer arrives per snapshot). Every
// per-block renderer (EdgeTube, SphereRings, BufferCamera — several call sites)
// independently decodes the SAME snapshot every frame; without this cache each one builds
// its own short-lived DataViews at 60fps under a ~430-700 snapshot/sec stream. This shares
// one decode per frame across all consumers. It moves no ownership — the memo just skips
// redoing pure arithmetic on unchanged input, exactly what memoization is for.
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
  const tick            = hdr.getUint32(0, true);
  const eventCount      = hdr.getUint32(4, true);
  const layoutLinkCount = hdr.getUint32(8, true);

  const layoutLinkBytes = layoutLinkCount * LAYOUT_LINK_STRIDE;
  const eventBytes      = eventCount * EVENT_STRIDE;
  // Either/or (Go's buildSnapshot, Buffer/pack.go): camera/overlay/scene are embedded
  // here ONLY in the fallback (no dedicated view fd). Distinguish the two shapes by
  // exact byte length — layoutLinkCount/eventCount are already known from the header,
  // so there is no ambiguity between "no view blocks" and "view blocks present".
  const viewBlockBytes = CAMERA_STRIDE + OVERLAY_STRIDE + SCENE_STRIDE;
  const withoutViewLen = BUF_HEADER_SIZE + layoutLinkBytes + eventBytes;
  const withViewLen    = withoutViewLen + viewBlockBytes;

  if (buf.byteLength !== withoutViewLen && buf.byteLength !== withViewLen) return null;

  let off = BUF_HEADER_SIZE;
  const layoutLinkView = new DataView(buf, off, layoutLinkBytes);
  off += layoutLinkBytes;

  let cameraView: DataView | null = null;
  let overlayView: DataView | null = null;
  let sceneView: DataView | null = null;

  if (buf.byteLength === withViewLen) {
    cameraView = new DataView(buf, off, CAMERA_STRIDE);
    off += CAMERA_STRIDE;
    overlayView = new DataView(buf, off, OVERLAY_STRIDE);
    off += OVERLAY_STRIDE;
    sceneView = new DataView(buf, off, SCENE_STRIDE);
    off += SCENE_STRIDE;
  }

  const eventView = new DataView(buf, off, eventBytes);

  return {
    tick, layoutLinkCount, layoutLinkView, cameraView, overlayView,
    sceneView, eventCount, eventView,
  };
}

/** Decoded view over a BUF_BLOCK_TAG_BEAD frame (see frame-tags.ts for its byte layout):
 *  [tick:u32][beadCount:u32] followed by beadCount × BEAD_STRIDE bead rows. */
export interface DecodedBeadFrame {
  tick: number;
  beadCount: number;
  /** DataView over the bead block only; byteLength = beadCount × BEAD_STRIDE. */
  beadView: DataView;
}

// Single-entry memo, mirroring decodeSnapshot's — the bead frame arrives every tick from
// its own tagged stream, independent of the scene frame's memo above.
let lastBeadBuf: ArrayBuffer | null = null;
let lastDecodedBead: DecodedBeadFrame | null = null;

/**
 * Decode a BUF_BLOCK_TAG_BEAD frame ArrayBuffer into a typed bead-row view.
 *
 * Returns null if the buffer is too small to be a valid bead frame (guards against
 * partial frames or empty buffers). Pure — no side effects, no store reads/writes. The
 * view aliases the original buffer (zero-copy). Memoized on `buf`'s identity, same
 * reasoning as decodeSnapshot's memo.
 */
export function decodeBeadFrame(buf: ArrayBuffer): DecodedBeadFrame | null {
  if (buf === lastBeadBuf) return lastDecodedBead;
  const decoded = decodeBeadFrameUncached(buf);
  lastBeadBuf = buf;
  lastDecodedBead = decoded;
  return decoded;
}

function decodeBeadFrameUncached(buf: ArrayBuffer): DecodedBeadFrame | null {
  if (buf.byteLength < BUF_BEAD_HEADER_SIZE) return null;

  const hdr = new DataView(buf, 0, BUF_BEAD_HEADER_SIZE);
  const tick      = hdr.getUint32(0, true);
  const beadCount = hdr.getUint32(4, true);

  const beadBytes = beadCount * BEAD_STRIDE;
  if (buf.byteLength < BUF_BEAD_HEADER_SIZE + beadBytes) return null;

  const beadView = new DataView(buf, BUF_BEAD_HEADER_SIZE, beadBytes);
  return { tick, beadCount, beadView };
}

/** Decoded view over a BUF_BLOCK_TAG_NODE frame (see frame-tags.ts for its byte layout):
 *  the Node/Interior/Port blocks + Label/PortName bytes — the node-owner-group blocks,
 *  which share one owner (the node movers) and travel together in their own tagged frame. */
export interface DecodedNodeFrame {
  tick: number;
  nodeCount: number;
  /** DataView over the node block only; byteLength = nodeCount × NODE_STRIDE. */
  nodeView: DataView;
  /** Interior grid rows (nodeCount × INTERIOR_SLOTS_PER_NODE); row = nodeRow*slots + slot. */
  interiorCount: number;
  /** DataView over the interior block; byteLength = interiorCount × INTERIOR_STRIDE. */
  interiorView: DataView;
  /** Total port rows across all nodes (self-sizing via the header portCount field). */
  portCount: number;
  /** DataView over the port block only; byteLength = portCount × PORT_STRIDE. Row i is the
   *  buffer port row i — the same index a port InstancedMesh instanceId carries for picking. */
  portView: DataView;
  /** Total bytes in the trailing label section (self-sizing via the header labelBytesCount). */
  labelBytesCount: number;
  /** Uint8 view over the label-bytes section: every node's label UTF-8 bytes concatenated in
   *  node-row order. A node's label is labelBytes[LabelOff : LabelOff+LabelLen) — see nodeLabel. */
  labelBytes: Uint8Array;
  /** Uint8 view over the port-name-bytes section (flattened port-row order). See portName. */
  portNameBytes: Uint8Array;
}

// Single-entry memo, mirroring decodeSnapshot's — the node frame arrives from its own
// tagged stream, independent of the scene frame's memo above.
let lastNodeBuf: ArrayBuffer | null = null;
let lastDecodedNode: DecodedNodeFrame | null = null;

/**
 * Decode a BUF_BLOCK_TAG_NODE frame ArrayBuffer into typed block views.
 *
 * Returns null if the buffer is too small to be a valid node frame (guards against
 * partial frames or empty buffers). Pure — no side effects, no store reads/writes. All
 * views alias the original buffer (zero-copy). Memoized on `buf`'s identity, same
 * reasoning as decodeSnapshot's memo.
 */
export function decodeNodeFrame(buf: ArrayBuffer): DecodedNodeFrame | null {
  if (buf === lastNodeBuf) return lastDecodedNode;
  const decoded = decodeNodeFrameUncached(buf);
  lastNodeBuf = buf;
  lastDecodedNode = decoded;
  return decoded;
}

function decodeNodeFrameUncached(buf: ArrayBuffer): DecodedNodeFrame | null {
  if (buf.byteLength < BUF_NODE_FRAME_HEADER_SIZE) return null;

  const hdr = new DataView(buf, 0, BUF_NODE_FRAME_HEADER_SIZE);
  const tick               = hdr.getUint32(0,  true);
  const nodeCount          = hdr.getUint32(4,  true);
  const portCount          = hdr.getUint32(8,  true);
  const labelBytesCount    = hdr.getUint32(12, true);
  const portNameBytesCount = hdr.getUint32(16, true);

  const interiorCount = nodeCount * INTERIOR_SLOTS_PER_NODE;

  const nodeBytes     = nodeCount * NODE_STRIDE;
  const interiorBytes = interiorCount * INTERIOR_STRIDE;
  const portBytes     = portCount * PORT_STRIDE;
  const expectedLen = BUF_NODE_FRAME_HEADER_SIZE + nodeBytes + interiorBytes + portBytes +
                      labelBytesCount + portNameBytesCount;

  if (buf.byteLength < expectedLen) return null;

  let off = BUF_NODE_FRAME_HEADER_SIZE;

  const nodeView = new DataView(buf, off, nodeBytes);
  off += nodeBytes;

  const interiorView = new DataView(buf, off, interiorBytes);
  off += interiorBytes;

  const portView = new DataView(buf, off, portBytes);
  off += portBytes;

  const labelBytes = new Uint8Array(buf, off, labelBytesCount);
  off += labelBytesCount;

  const portNameBytes = new Uint8Array(buf, off, portNameBytesCount);

  return {
    tick, nodeCount, nodeView, interiorCount, interiorView, portCount, portView,
    labelBytesCount, labelBytes, portNameBytes,
  };
}

/**
 * Human label for buffer node row `row`: slice [LabelOff, LabelOff+LabelLen) out of the
 * decoded label-bytes section and UTF-8 decode it. Returns "" when the node has no label
 * (LabelLen == 0). Pure — reads only the decoded node frame. This is the row-keyed
 * replacement for the removed id/label sidecar: the label rides the binary buffer.
 */
export function nodeLabel(decoded: DecodedNodeFrame, row: number): string {
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
export function portName(decoded: DecodedNodeFrame, row: number): string {
  if (row < 0) return "";
  const off = readPortPortNameOff(decoded.portView, row);
  const len = readPortPortNameLen(decoded.portView, row);
  if (len === 0) return "";
  if (off < 0 || len < 0 || off + len > decoded.portNameBytes.byteLength) return "";
  return STR_DECODER.decode(decoded.portNameBytes.subarray(off, off + len));
}

/** Decoded view over a BUF_BLOCK_TAG_EDGE frame (see frame-tags.ts for its byte layout):
 *  the Edge block + EdgeLabel bytes — the Edge block carries NO endpoint coordinates; it
 *  references its two port rows (SrcPortRow/DstPortRow), resolved against the SAME TICK's
 *  NODE frame's Port block (see EdgeTube.tsx). */
export interface DecodedEdgeFrame {
  tick: number;
  edgeCount: number;
  /** DataView over the edge block only; byteLength = edgeCount × EDGE_STRIDE. */
  edgeView: DataView;
  /** Uint8 view over the edge-label-bytes section (edge-row order). See edgeLabel. */
  edgeLabelBytes: Uint8Array;
}

// Single-entry memo, mirroring decodeSnapshot's — the edge frame arrives from its own
// tagged stream, independent of the scene frame's memo above.
let lastEdgeBuf: ArrayBuffer | null = null;
let lastDecodedEdge: DecodedEdgeFrame | null = null;

/**
 * Decode a BUF_BLOCK_TAG_EDGE frame ArrayBuffer into a typed Edge-block view.
 *
 * Returns null if the buffer is too small to be a valid edge frame (guards against
 * partial frames or empty buffers). Pure — no side effects, no store reads/writes. The
 * view aliases the original buffer (zero-copy). Memoized on `buf`'s identity, same
 * reasoning as decodeSnapshot's memo.
 */
export function decodeEdgeFrame(buf: ArrayBuffer): DecodedEdgeFrame | null {
  if (buf === lastEdgeBuf) return lastDecodedEdge;
  const decoded = decodeEdgeFrameUncached(buf);
  lastEdgeBuf = buf;
  lastDecodedEdge = decoded;
  return decoded;
}

function decodeEdgeFrameUncached(buf: ArrayBuffer): DecodedEdgeFrame | null {
  if (buf.byteLength < BUF_EDGE_FRAME_HEADER_SIZE) return null;

  const hdr = new DataView(buf, 0, BUF_EDGE_FRAME_HEADER_SIZE);
  const tick                = hdr.getUint32(0, true);
  const edgeCount           = hdr.getUint32(4, true);
  const edgeLabelBytesCount = hdr.getUint32(8, true);

  const edgeBytes = edgeCount * EDGE_STRIDE;
  const expectedLen = BUF_EDGE_FRAME_HEADER_SIZE + edgeBytes + edgeLabelBytesCount;
  if (buf.byteLength < expectedLen) return null;

  let off = BUF_EDGE_FRAME_HEADER_SIZE;

  const edgeView = new DataView(buf, off, edgeBytes);
  off += edgeBytes;

  const edgeLabelBytes = new Uint8Array(buf, off, edgeLabelBytesCount);

  return { tick, edgeCount, edgeView, edgeLabelBytes };
}

/** Decoded view over ONE edge's dedicated per-fd stream frame (BUF_BLOCK_TAG_EDGE_STREAM —
 *  see frame-tags.ts's BUF_EDGE_STREAM_FRAME_HEADER_SIZE doc comment for the byte layout):
 *  [tick:u32] + one EDGE_STRIDE row (this edge's own SrcPortRow/DstPortRow/Selected) + this
 *  edge's own label bytes (inline, not a shared section) + [beadCount:u32] + beadCount ×
 *  BEAD_STRIDE bead rows (this edge's wire's own live in-flight beads). */
export interface DecodedEdgeStreamFrame {
  tick: number;
  /** DataView over the single Edge row (row 0); byteLength = EDGE_STRIDE. */
  edgeView: DataView;
  /** This edge's own label, decoded straight from its inline bytes (no shared section / no
   *  Off into a foreign frame — unlike the fd-3 Edge block's EdgeLabelOff/Len). */
  label: string;
  beadCount: number;
  /** DataView over this edge's own bead rows; byteLength = beadCount × BEAD_STRIDE. */
  beadView: DataView;
}

// Per-edge-row memo (keyed by row, not a single lastBuf — many edge streams arrive
// concurrently, one per fd, so a single-entry memo would thrash across rows every frame).
const lastEdgeStreamBufByRow = new Map<number, ArrayBuffer>();
const lastDecodedEdgeStreamByRow = new Map<number, DecodedEdgeStreamFrame | null>();

/**
 * Decode ONE edge row's BUF_BLOCK_TAG_EDGE_STREAM frame ArrayBuffer into a typed view.
 * Returns null if the buffer is too small to be a valid frame. Pure — no side effects
 * beyond this function's own per-row memo. Views alias the original buffer (zero-copy).
 */
export function decodeEdgeStreamFrame(row: number, buf: ArrayBuffer): DecodedEdgeStreamFrame | null {
  if (lastEdgeStreamBufByRow.get(row) === buf) {
    return lastDecodedEdgeStreamByRow.get(row) ?? null;
  }
  const decoded = decodeEdgeStreamFrameUncached(buf);
  lastEdgeStreamBufByRow.set(row, buf);
  lastDecodedEdgeStreamByRow.set(row, decoded);
  return decoded;
}

function decodeEdgeStreamFrameUncached(buf: ArrayBuffer): DecodedEdgeStreamFrame | null {
  if (buf.byteLength < BUF_EDGE_STREAM_FRAME_HEADER_SIZE + EDGE_STRIDE) return null;
  const hdr = new DataView(buf, 0, 4);
  const tick = hdr.getUint32(0, true);

  let off = BUF_EDGE_STREAM_FRAME_HEADER_SIZE;
  const edgeView = new DataView(buf, off, EDGE_STRIDE);
  off += EDGE_STRIDE;

  // EdgeLabelLen lives on the Edge row itself (readEdgeEdgeLabelLen) — EdgeLabelOff is
  // always 0 on a dedicated per-edge stream (this frame's own label bytes immediately
  // follow the row, no shared section — see Buffer/edge_stream_frame.go).
  const labelLen = readEdgeEdgeLabelLen(edgeView, 0);
  if (buf.byteLength < off + labelLen + 4) return null;
  const labelBytes = new Uint8Array(buf, off, labelLen);
  const label = STR_DECODER.decode(labelBytes);
  off += labelLen;

  const beadCountView = new DataView(buf, off, 4);
  const beadCount = beadCountView.getUint32(0, true);
  off += 4;

  const beadBytes = beadCount * BEAD_STRIDE;
  if (buf.byteLength < off + beadBytes) return null;
  const beadView = new DataView(buf, off, beadBytes);

  return { tick, edgeView, label, beadCount, beadView };
}

/** Decoded view over a BUF_BLOCK_TAG_VIEW frame (see frame-tags.ts for its byte layout):
 *  [tick:u32] followed by the Camera, Overlay, and Scene blocks. */
export interface DecodedViewFrame {
  tick: number;
  cameraView: DataView;
  overlayView: DataView;
  sceneView: DataView;
}

// Single-entry memo, mirroring decodeSnapshot's — the view frame arrives on its own
// dedicated fd, independent of the scene frame's memo above.
let lastViewBuf: ArrayBuffer | null = null;
let lastDecodedView: DecodedViewFrame | null = null;

/**
 * Decode a BUF_BLOCK_TAG_VIEW frame ArrayBuffer (the dedicated view-fd stream) into
 * typed camera/overlay/scene views. Returns null if the buffer is too small to be a
 * valid view frame. Pure — no side effects, no store reads/writes. Views alias the
 * original buffer (zero-copy). Memoized on `buf`'s identity.
 */
export function decodeViewFrame(buf: ArrayBuffer): DecodedViewFrame | null {
  if (buf === lastViewBuf) return lastDecodedView;
  const decoded = decodeViewFrameUncached(buf);
  lastViewBuf = buf;
  lastDecodedView = decoded;
  return decoded;
}

function decodeViewFrameUncached(buf: ArrayBuffer): DecodedViewFrame | null {
  const expectedLen = BUF_VIEW_FRAME_HEADER_SIZE + CAMERA_STRIDE + OVERLAY_STRIDE + SCENE_STRIDE;
  if (buf.byteLength < expectedLen) return null;

  const tick = new DataView(buf, 0, BUF_VIEW_FRAME_HEADER_SIZE).getUint32(0, true);
  let off = BUF_VIEW_FRAME_HEADER_SIZE;

  const cameraView = new DataView(buf, off, CAMERA_STRIDE);
  off += CAMERA_STRIDE;

  const overlayView = new DataView(buf, off, OVERLAY_STRIDE);
  off += OVERLAY_STRIDE;

  const sceneView = new DataView(buf, off, SCENE_STRIDE);

  return { tick, cameraView, overlayView, sceneView };
}

/**
 * Edge label for buffer edge row `row`: slice out of the decoded edge-label-bytes section.
 * Returns "" when the edge has no label. Used only by the buffer-decoded .probe logger — the
 * render/bridge path resolves an edge hit by row index, never by this string.
 */
export function edgeLabel(decoded: DecodedEdgeFrame, row: number): string {
  if (row < 0) return "";
  const off = readEdgeEdgeLabelOff(decoded.edgeView, row);
  const len = readEdgeEdgeLabelLen(decoded.edgeView, row);
  if (len === 0) return "";
  if (off < 0 || len < 0 || off + len > decoded.edgeLabelBytes.byteLength) return "";
  return STR_DECODER.decode(decoded.edgeLabelBytes.subarray(off, off + len));
}
