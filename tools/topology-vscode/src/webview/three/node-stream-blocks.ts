// node-stream-blocks.ts — the per-node dedicated-stream either/or, mirroring
// edge-stream-blocks.ts's role for the per-edge streams (memory/feedback_no_single_writer_bridge.md).
//
// Every render-tree consumer that used to read getLatestNodeFrame()+decodeNodeFrame() (the
// fd-3 combined Node/Interior/Port + Label/PortName frame) now reads getNodeFrameOrFallback()
// instead — ONE function returning the SAME DecodedNodeFrame shape, so no consumer's read
// logic (row indexing, column readers) has to change. When the dedicated per-node NODE and
// INTERIOR streams are active (WIREFOLD_STREAM_FDS carries "node"+"interior"), this
// AGGREGATES every node row's own frame (Buffer.BuildNodeStreamFrame /
// BuildInteriorStreamFrame — one goroutine's fd each) into one contiguous
// DecodedNodeFrame-shaped view, rewriting only the two offset columns
// (LabelOff/PortNameOff) that must point into the aggregated label/port-name byte
// sections instead of each frame's own inline bytes. This IS a byte copy (the source
// bytes live in N separate ArrayBuffers, one per node/interior fd) — but it happens once
// per (nodeStreamVersion, interiorStreamVersion) change, not once per render-tree consumer
// per frame (of which there are ~8), via the module-level memo below.
//
// A node row with no NODE-stream frame yet (arrived out of order at startup) is treated as
// an all-zero row (radius 0 falls back to NODE_SPHERE_RADIUS in the renderers via `|| `,
// portCount 0) — the same "unresolved" treatment edge-stream-blocks.ts gives a missing row.
// A node row with no INTERIOR-stream frame yet is treated as all-Present=0 (no interior
// beads drawn for that node until its own Update goroutine's first frame arrives).
//
// Falls back to the fd-3 combined Node frame (decodeNodeFrame(getLatestNodeFrame())) when
// no dedicated node-stream frame has arrived — the required dual-path (env unset, headless
// tests, non-extension launches).

import { getLatestNodeFrame, getLatestNodeStreamFrames, getLatestInteriorStreamFrames, getNodeStreamVersion, getInteriorStreamVersion, subscribeNodeFrame, subscribeNodeStreamFrame, subscribeInteriorStreamFrame } from "../snapshot-buffer";
import {
  decodeNodeFrame, decodeNodeStreamFrame, decodeInteriorStreamFrame,
  readNodeStreamLayoutLinkDstNodeRow, readNodeStreamLayoutLinkEdgeRow,
  type DecodedNodeFrame,
} from "./buffer-decode";
import {
  NODE_STRIDE, PORT_STRIDE, INTERIOR_STRIDE, INTERIOR_SLOTS_PER_NODE,
  NODE_COL_LABEL_OFF, NODE_COL_LABEL_LEN,
  PORT_COL_PORT_NAME_OFF, PORT_COL_PORT_NAME_LEN,
  LAYOUT_LINK_STRIDE, LAYOUT_LINK_COL_SRC_NODE_ROW, LAYOUT_LINK_COL_DST_NODE_ROW, LAYOUT_LINK_COL_EDGE_ROW,
} from "../../schema/buffer-layout";

const STR_ENCODER = new TextEncoder();

// Memo keyed on the (nodeStreamVersion, interiorStreamVersion) pair — both bump on every
// setLatestNodeStreamFrame/setLatestInteriorStreamFrame call (snapshot-buffer.ts), so an
// unchanged pair means every consumer this render tick reads the SAME aggregate rather
// than each re-copying the same bytes.
let lastNodeVersion = -1;
let lastInteriorVersion = -1;
let lastAggregate: DecodedNodeFrame | null = null;

/**
 * getNodeFrameOrFallback returns the current Node/Interior/Port(+Label/PortName) view,
 * from the aggregated per-node dedicated streams if active, else the fd-3 combined Node
 * frame — the required fallback. Pure read (aside from its own memo) — no store writes.
 */
export function getNodeFrameOrFallback(): DecodedNodeFrame | null {
  const nodeFrames = getLatestNodeStreamFrames();
  if (nodeFrames.size === 0) {
    const fallback = getLatestNodeFrame();
    return fallback ? decodeNodeFrame(fallback) : null;
  }
  const nv = getNodeStreamVersion();
  const iv = getInteriorStreamVersion();
  if (nv === lastNodeVersion && iv === lastInteriorVersion && lastAggregate) {
    return lastAggregate;
  }
  const aggregate = buildAggregate(nodeFrames, getLatestInteriorStreamFrames());
  lastNodeVersion = nv;
  lastInteriorVersion = iv;
  lastAggregate = aggregate;
  return aggregate;
}

/** Shape of the LayoutLink block the LayoutLink overlay (EdgeTube.tsx) consumes — the SAME
 *  shape the fd-3 scene frame's LayoutLink block produces (SrcNodeRow/DstNodeRow/EdgeRow,
 *  LAYOUT_LINK_STRIDE-byte rows), so EdgeTube's read logic doesn't have to change. */
export interface LayoutLinkAgg {
  layoutLinkCount: number;
  layoutLinkView: DataView;
}

let lastLayoutLinkVersion = -1;
let lastLayoutLinkAgg: LayoutLinkAgg | null = null;

/**
 * getLayoutLinksOrFallback returns the current LAYOUT-link overlay pairs: aggregated from
 * every per-node dedicated NODE stream's own outbound layout-links (each node streams the
 * pairs for which it is the SOURCE — see node_mover.go's layoutLinkTos) when those streams
 * are active, else the fd-3 scene frame's shared LayoutLink block (sceneLayoutLinkCount/
 * sceneLayoutLinkView, the required fallback — pass decodeSnapshot's own fields straight
 * through). Reconstructs full SrcNodeRow/DstNodeRow/EdgeRow rows (SrcNodeRow = the node
 * row whose own frame carried that entry) so the aggregate is BYTE-COMPATIBLE with the
 * pre-migration shared block.
 */
export function getLayoutLinksOrFallback(sceneLayoutLinkCount: number, sceneLayoutLinkView: DataView): LayoutLinkAgg {
  const nodeFrames = getLatestNodeStreamFrames();
  if (nodeFrames.size === 0) {
    return { layoutLinkCount: sceneLayoutLinkCount, layoutLinkView: sceneLayoutLinkView };
  }
  const nv = getNodeStreamVersion();
  if (nv === lastLayoutLinkVersion && lastLayoutLinkAgg) {
    return lastLayoutLinkAgg;
  }

  let maxRow = -1;
  for (const r of nodeFrames.keys()) if (r > maxRow) maxRow = r;
  const nodeCount = maxRow + 1;

  const srcRows: number[] = [];
  const dstRows: number[] = [];
  const edgeRows: number[] = [];
  for (let row = 0; row < nodeCount; row++) {
    const buf = nodeFrames.get(row);
    if (!buf) continue;
    const decoded = decodeNodeStreamFrame(row, buf);
    if (!decoded) continue;
    for (let i = 0; i < decoded.layoutLinkCount; i++) {
      srcRows.push(row);
      dstRows.push(readNodeStreamLayoutLinkDstNodeRow(decoded.layoutLinkView, i));
      edgeRows.push(readNodeStreamLayoutLinkEdgeRow(decoded.layoutLinkView, i));
    }
  }

  const layoutLinkCount = srcRows.length;
  const layoutLinkView = new DataView(new ArrayBuffer(layoutLinkCount * LAYOUT_LINK_STRIDE));
  for (let i = 0; i < layoutLinkCount; i++) {
    const off = i * LAYOUT_LINK_STRIDE;
    layoutLinkView.setInt32(off + LAYOUT_LINK_COL_SRC_NODE_ROW, srcRows[i]!, true);
    layoutLinkView.setInt32(off + LAYOUT_LINK_COL_DST_NODE_ROW, dstRows[i]!, true);
    layoutLinkView.setInt32(off + LAYOUT_LINK_COL_EDGE_ROW, edgeRows[i]!, true);
  }

  const agg: LayoutLinkAgg = { layoutLinkCount, layoutLinkView };
  lastLayoutLinkVersion = nv;
  lastLayoutLinkAgg = agg;
  return agg;
}

/** Subscribe to either source updating (subscribe-fn shape, e.g. for a React external-store
 *  hook) — the per-node analogue of view-blocks.ts's subscribeViewBlocks. */
export function subscribeNodeStreamBlocks(fn: () => void): () => void {
  const unsubFallback = subscribeNodeFrame(fn);
  const unsubNode = subscribeNodeStreamFrame(fn);
  const unsubInterior = subscribeInteriorStreamFrame(fn);
  return () => {
    unsubFallback();
    unsubNode();
    unsubInterior();
  };
}

function buildAggregate(
  nodeFrames: ReadonlyMap<number, ArrayBuffer>,
  interiorFrames: ReadonlyMap<number, ArrayBuffer>,
): DecodedNodeFrame {
  // edgeCount-style sizing (edge-stream-blocks.ts's getEdgeStreamAccessor): one past the
  // highest row that has posted a frame, NOT frames.size — a sparse row set (arrived out
  // of order at startup) must not be misnumbered as a dense 0..size-1 range.
  let maxRow = -1;
  for (const r of nodeFrames.keys()) if (r > maxRow) maxRow = r;
  const nodeCount = maxRow + 1;

  const decodedByRow = new Map<number, ReturnType<typeof decodeNodeStreamFrame>>();
  let totalPortCount = 0;
  let totalLabelBytes = 0;
  let totalPortNameBytes = 0;
  for (let row = 0; row < nodeCount; row++) {
    const buf = nodeFrames.get(row);
    const decoded = buf ? decodeNodeStreamFrame(row, buf) : null;
    decodedByRow.set(row, decoded);
    if (decoded) {
      totalPortCount += decoded.portCount;
      totalLabelBytes += STR_ENCODER.encode(decoded.label).length;
      totalPortNameBytes += decoded.portNameBytes.byteLength;
    }
  }

  const interiorCount = nodeCount * INTERIOR_SLOTS_PER_NODE;
  const nodeBytes = nodeCount * NODE_STRIDE;
  const interiorBytes = interiorCount * INTERIOR_STRIDE;
  const portBytes = totalPortCount * PORT_STRIDE;

  const nodeBuf = new ArrayBuffer(nodeBytes);
  const nodeOut = new DataView(nodeBuf);
  const interiorBuf = new ArrayBuffer(interiorBytes);
  const interiorOut = new Uint8Array(interiorBuf);
  const portBuf = new ArrayBuffer(portBytes);
  const portOut = new Uint8Array(portBuf);
  const labelBytesOut = new Uint8Array(totalLabelBytes);
  const portNameBytesOut = new Uint8Array(totalPortNameBytes);

  let labelCursor = 0;
  let portNameCursor = 0;
  let portCursor = 0;

  for (let row = 0; row < nodeCount; row++) {
    const decoded = decodedByRow.get(row) ?? null;
    const nodeRowBytes = new Uint8Array(nodeBuf, row * NODE_STRIDE, NODE_STRIDE);
    if (decoded) {
      // Copy this node's own NODE_STRIDE row verbatim, then rewrite LabelOff to point
      // into the aggregated label-bytes section (LabelLen is already correct — it came
      // straight from this row's own bytes, unchanged by the copy).
      nodeRowBytes.set(new Uint8Array(decoded.nodeView.buffer, decoded.nodeView.byteOffset, NODE_STRIDE));
      const labelEncoded = STR_ENCODER.encode(decoded.label);
      nodeOut.setUint32(row * NODE_STRIDE + NODE_COL_LABEL_OFF, labelCursor, true);
      nodeOut.setUint32(row * NODE_STRIDE + NODE_COL_LABEL_LEN, labelEncoded.length, true);
      labelBytesOut.set(labelEncoded, labelCursor);
      labelCursor += labelEncoded.length;

      // Port rows: NodeRow column is already the global node row (BuildNodeStreamFrame
      // stamps it), so the raw bytes carry over verbatim except PortNameOff, which must
      // point into the aggregated port-name-bytes section.
      for (let p = 0; p < decoded.portCount; p++) {
        const srcOff = p * PORT_STRIDE;
        const rowBytes = new Uint8Array(decoded.portView.buffer, decoded.portView.byteOffset + srcOff, PORT_STRIDE);
        portOut.set(rowBytes, portCursor * PORT_STRIDE);
        const nameOff = decoded.portView.getUint32(srcOff + PORT_COL_PORT_NAME_OFF, true);
        const nameLen = decoded.portView.getUint32(srcOff + PORT_COL_PORT_NAME_LEN, true);
        const portOutView = new DataView(portBuf, portCursor * PORT_STRIDE, PORT_STRIDE);
        portOutView.setUint32(PORT_COL_PORT_NAME_OFF, portNameCursor, true);
        portOutView.setUint32(PORT_COL_PORT_NAME_LEN, nameLen, true);
        portNameBytesOut.set(decoded.portNameBytes.subarray(nameOff, nameOff + nameLen), portNameCursor);
        portNameCursor += nameLen;
        portCursor++;
      }
    }
    // A row with no frame yet stays all-zero (nodeRowBytes is already zero-initialized by
    // `new ArrayBuffer`) — the "unresolved" treatment this file's header comment describes.

    const interiorRowBytes = new Uint8Array(interiorBuf, row * INTERIOR_SLOTS_PER_NODE * INTERIOR_STRIDE, INTERIOR_SLOTS_PER_NODE * INTERIOR_STRIDE);
    const interiorFrameBuf = interiorFrames.get(row);
    const interiorDecoded = interiorFrameBuf ? decodeInteriorStreamFrame(row, interiorFrameBuf) : null;
    if (interiorDecoded) {
      interiorRowBytes.set(new Uint8Array(
        interiorDecoded.interiorView.buffer,
        interiorDecoded.interiorView.byteOffset,
        INTERIOR_SLOTS_PER_NODE * INTERIOR_STRIDE,
      ));
    }
    // Missing interior frame ⇒ stays all-zero (Present=0 for every slot) — no interior
    // beads drawn for this node until its own Update goroutine's first frame lands.
  }

  return {
    tick: 0,
    nodeCount,
    nodeView: nodeOut,
    interiorCount,
    interiorView: new DataView(interiorBuf),
    portCount: totalPortCount,
    portView: new DataView(portBuf),
    labelBytesCount: totalLabelBytes,
    labelBytes: labelBytesOut,
    portNameBytes: portNameBytesOut,
  };
}
