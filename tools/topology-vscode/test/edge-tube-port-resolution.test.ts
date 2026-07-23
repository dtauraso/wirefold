// edge-tube-port-resolution.test.ts — proves EdgeTube.tsx's tear-free port-row →
// coordinate resolution: an edge's SrcPortRow/DstPortRow (from the per-edge dedicated
// stream, edge-stream-blocks.ts) resolve against the per-NODE dedicated streams'
// AGGREGATED Port block (node-stream-blocks.ts) — the SAME live coordinates
// NodeInstances/PortInstances read — with no separate "Edge frame's own endpoint copy"
// to go stale. This mirrors EdgeTube.tsx's `portEndpoint` helper exactly (read the port's
// PX/PY/PZ off the resolved node-stream Port block), without mounting a React component.

import { describe, it, expect, vi } from "vitest";
import {
  BUF_EDGE_STREAM_FRAME_HEADER_SIZE,
  BUF_NODE_STREAM_FRAME_HEADER_SIZE,
  BUF_INTERIOR_STREAM_FRAME_HEADER_SIZE,
} from "../src/schema/frame-tags";
import {
  EDGE_STRIDE, EDGE_COL_SRC_PORT_ROW, EDGE_COL_DST_PORT_ROW,
  NODE_STRIDE, PORT_STRIDE, INTERIOR_STRIDE, INTERIOR_SLOTS_PER_NODE,
  PORT_COL_NODE_ROW, PORT_COL_PX, PORT_COL_PY, PORT_COL_PZ,
  PORT_COL_PORT_NAME_OFF, PORT_COL_PORT_NAME_LEN,
  readPortPX, readPortPY, readPortPZ,
} from "../src/schema/buffer-layout";

async function freshModules() {
  vi.resetModules();
  const snapshotBuffer = await import("../src/webview/snapshot-buffer");
  const nodeStreamBlocks = await import("../src/webview/three/node-stream-blocks");
  const edgeStreamBlocks = await import("../src/webview/three/edge-stream-blocks");
  return { snapshotBuffer, nodeStreamBlocks, edgeStreamBlocks };
}

/** Build one edge's BUF_BLOCK_TAG_EDGE_STREAM frame: [tick] + 1 EDGE_STRIDE row (label
 *  len 0) + [beadCount=0]. */
function makeEdgeStreamFrame(srcPortRow: number, dstPortRow: number): ArrayBuffer {
  const total = BUF_EDGE_STREAM_FRAME_HEADER_SIZE + EDGE_STRIDE + 4; // + beadCount
  const buf = new ArrayBuffer(total);
  const dv = new DataView(buf);
  dv.setUint32(0, 1, true); // tick
  let off = BUF_EDGE_STREAM_FRAME_HEADER_SIZE;
  dv.setInt32(off + EDGE_COL_SRC_PORT_ROW, srcPortRow, true);
  dv.setInt32(off + EDGE_COL_DST_PORT_ROW, dstPortRow, true);
  // EdgeLabelLen stays 0 (default) — no label bytes.
  off += EDGE_STRIDE;
  dv.setUint32(off, 0, true); // beadCount = 0
  return buf;
}

/** Build one node's BUF_BLOCK_TAG_NODE_STREAM frame with a single port at (px,py,pz). */
function makeNodeStreamFrame(nodeRow: number, px: number, py: number, pz: number): ArrayBuffer {
  const total = BUF_NODE_STREAM_FRAME_HEADER_SIZE + NODE_STRIDE + PORT_STRIDE;
  const buf = new ArrayBuffer(total);
  const dv = new DataView(buf);
  dv.setUint32(0, 1, true); // tick
  dv.setUint32(4, 1, true); // portCount = 1
  dv.setUint32(8, 0, true); // labelLen = 0
  dv.setUint32(12, 0, true); // portNameBytesCount = 0
  let off = BUF_NODE_STREAM_FRAME_HEADER_SIZE + NODE_STRIDE; // skip Node row, all-zero fine
  dv.setInt32(off + PORT_COL_NODE_ROW, nodeRow, true);
  dv.setFloat32(off + PORT_COL_PX, px, true);
  dv.setFloat32(off + PORT_COL_PY, py, true);
  dv.setFloat32(off + PORT_COL_PZ, pz, true);
  dv.setUint32(off + PORT_COL_PORT_NAME_OFF, 0, true);
  dv.setUint32(off + PORT_COL_PORT_NAME_LEN, 0, true);
  return buf;
}

function emptyInteriorFrame(): ArrayBuffer {
  const bytes = INTERIOR_SLOTS_PER_NODE * INTERIOR_STRIDE;
  return new ArrayBuffer(BUF_INTERIOR_STREAM_FRAME_HEADER_SIZE + bytes);
}

/** Mirrors EdgeTube.tsx's portEndpoint helper: read PX/PY/PZ off the resolved node-stream
 *  Port block for a given port row (-1 → origin, matching EdgeTube's unresolved case). */
function portEndpoint(portView: DataView, portRow: number): [number, number, number] {
  if (portRow < 0) return [0, 0, 0];
  return [readPortPX(portView, portRow), readPortPY(portView, portRow), readPortPZ(portView, portRow)];
}

describe("EdgeTube tear-free port-row resolution", () => {
  it("resolves an edge's SrcPortRow/DstPortRow against the aggregated per-node Port block", async () => {
    const { snapshotBuffer, nodeStreamBlocks, edgeStreamBlocks } = await freshModules();

    // Node 0 owns port row 0 at (10,0,0); node 1 owns port row 1 at (50,0,0).
    snapshotBuffer.setLatestNodeStreamFrame(0, makeNodeStreamFrame(0, 10, 0, 0));
    snapshotBuffer.setLatestNodeStreamFrame(1, makeNodeStreamFrame(1, 50, 0, 0));
    snapshotBuffer.setLatestInteriorStreamFrame(0, emptyInteriorFrame());
    snapshotBuffer.setLatestInteriorStreamFrame(1, emptyInteriorFrame());

    // Edge row 0 connects port row 0 (src) to port row 1 (dst).
    snapshotBuffer.setLatestEdgeStreamFrame(0, makeEdgeStreamFrame(0, 1));

    const edgeAccessor = edgeStreamBlocks.getEdgeStreamAccessor()!;
    expect(edgeAccessor).not.toBeNull();
    const decodedNode = nodeStreamBlocks.getNodeFrameOrFallback()!;
    expect(decodedNode).not.toBeNull();

    const srcRow = edgeAccessor.srcPortRow(0);
    const dstRow = edgeAccessor.dstPortRow(0);
    const [sx, sy, sz] = portEndpoint(decodedNode.portView, srcRow);
    const [ex, ey, ez] = portEndpoint(decodedNode.portView, dstRow);
    expect([sx, sy, sz]).toEqual([10, 0, 0]);
    expect([ex, ey, ez]).toEqual([50, 0, 0]);
  });

  it("stays tear-free across a node move: a re-emitted NODE-stream frame is immediately\n" +
     "     visible through the SAME aggregate the edge resolves against, no stale copy", async () => {
    const { snapshotBuffer, nodeStreamBlocks, edgeStreamBlocks } = await freshModules();

    snapshotBuffer.setLatestNodeStreamFrame(0, makeNodeStreamFrame(0, 10, 0, 0));
    snapshotBuffer.setLatestNodeStreamFrame(1, makeNodeStreamFrame(1, 50, 0, 0));
    snapshotBuffer.setLatestInteriorStreamFrame(0, emptyInteriorFrame());
    snapshotBuffer.setLatestInteriorStreamFrame(1, emptyInteriorFrame());
    snapshotBuffer.setLatestEdgeStreamFrame(0, makeEdgeStreamFrame(0, 1));

    const edgeAccessor = edgeStreamBlocks.getEdgeStreamAccessor()!;
    const before = nodeStreamBlocks.getNodeFrameOrFallback()!;
    expect(readPortPX(before.portView, edgeAccessor.dstPortRow(0))).toBe(50);

    // Node 1's nodeMover re-emits a fresh frame after a drag — port 1 moves to (99,0,0).
    // The EDGE frame is untouched (edges carry no coordinates — see BuildEdgeStreamFrame).
    snapshotBuffer.setLatestNodeStreamFrame(1, makeNodeStreamFrame(1, 99, 0, 0));

    const after = nodeStreamBlocks.getNodeFrameOrFallback()!;
    expect(readPortPX(after.portView, edgeAccessor.dstPortRow(0))).toBe(99);
  });
});
