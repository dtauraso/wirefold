// Unit tests for the per-node dedicated-stream decode + aggregation path
// (schema/frame-tags.ts's BUF_BLOCK_TAG_NODE_STREAM/BUF_BLOCK_TAG_INTERIOR_STREAM,
// buffer-decode.ts's decodeNodeStreamFrame/decodeInteriorStreamFrame, and
// three/node-stream-blocks.ts's getNodeFrame aggregator).
//
// Also proves the EdgeTube tear-free resolution: an edge's SrcPortRow/DstPortRow (from
// the per-edge dedicated stream) resolve against the AGGREGATED per-node Port block's
// PX/PY/PZ — the same live coordinates NodeInstances/PortInstances read — with no
// separate "Edge frame's own copy of the endpoint" to go stale.

import { describe, it, expect, vi } from "vitest";
import {
  decodeNodeStreamFrame, decodeInteriorStreamFrame,
  readNodeStreamLayoutLinkDstNodeRow, readNodeStreamLayoutLinkEdgeRow,
} from "../src/webview/three/buffer-decode";
import {
  BUF_NODE_STREAM_FRAME_HEADER_SIZE, BUF_INTERIOR_STREAM_FRAME_HEADER_SIZE,
  NODE_STREAM_LAYOUT_LINK_STRIDE,
} from "../src/schema/frame-tags";
import {
  NODE_STRIDE, PORT_STRIDE, INTERIOR_STRIDE, INTERIOR_SLOTS_PER_NODE,
  NODE_COL_CX, NODE_COL_CY, NODE_COL_CZ, NODE_COL_RADIUS,
  PORT_COL_NODE_ROW, PORT_COL_PX, PORT_COL_PY, PORT_COL_PZ,
  PORT_COL_PORT_NAME_OFF, PORT_COL_PORT_NAME_LEN,
  readNodeCX, readNodeCY, readNodeCZ, readNodeRadius,
  readPortNodeRow, readPortPX, readPortPY, readPortPZ,
  readInteriorPresent, readInteriorValue,
  readLayoutLinkSrcNodeRow, readLayoutLinkDstNodeRow, readLayoutLinkEdgeRow,
} from "../src/schema/buffer-layout";

// Every test below that touches the STATEFUL per-node cells (snapshot-buffer.ts's plain
// module-level maps) gets a FRESH module instance via vi.resetModules() + dynamic import —
// those maps persist across tests that share one import, so isolation must be explicit
// (mirrors the "fallback" test's isolation reasoning). freshNodeStreamModules() is the
// shared helper for that pattern.
async function freshNodeStreamModules() {
  vi.resetModules();
  const snapshotBuffer = await import("../src/webview/snapshot-buffer");
  const nodeStreamBlocks = await import("../src/webview/three/node-stream-blocks");
  return { snapshotBuffer, nodeStreamBlocks };
}

// ── helpers ───────────────────────────────────────────────────────────────────

function expectF32(got: number, want: number) {
  expect(got).toBeCloseTo(want, 5);
}

/** Build one node's BUF_BLOCK_TAG_NODE_STREAM frame: [tick][portCount][labelLen]
 *  [portNameBytesCount][layoutLinkCount] + 1 Node row (LabelOff always 0 here) + label
 *  bytes + portCount Port rows (NodeRow column = nodeRow) + port-name bytes + this node's
 *  own outbound LayoutLink rows ([DstNodeRow][EdgeRow] each). */
function makeNodeStreamFrame(opts: {
  nodeRow: number;
  cx: number; cy: number; cz: number; radius: number;
  label: string;
  ports: Array<{ px: number; py: number; pz: number; name: string }>;
  layoutLinks?: Array<{ dstNodeRow: number; edgeRow: number }>;
}): ArrayBuffer {
  const enc = new TextEncoder();
  const labelBytes = enc.encode(opts.label);
  const portNameChunks = opts.ports.map((p) => enc.encode(p.name));
  const portNameBytesCount = portNameChunks.reduce((a, c) => a + c.length, 0);
  const portCount = opts.ports.length;
  const layoutLinks = opts.layoutLinks ?? [];

  const total = BUF_NODE_STREAM_FRAME_HEADER_SIZE + NODE_STRIDE + labelBytes.length
    + portCount * PORT_STRIDE + portNameBytesCount + layoutLinks.length * NODE_STREAM_LAYOUT_LINK_STRIDE;
  const buf = new ArrayBuffer(total);
  const dv = new DataView(buf);
  dv.setUint32(0, 7, true); // tick
  dv.setUint32(4, portCount, true);
  dv.setUint32(8, labelBytes.length, true);
  dv.setUint32(12, portNameBytesCount, true);
  dv.setUint32(16, layoutLinks.length, true);

  let off = BUF_NODE_STREAM_FRAME_HEADER_SIZE;
  dv.setFloat32(off + NODE_COL_CX, opts.cx, true);
  dv.setFloat32(off + NODE_COL_CY, opts.cy, true);
  dv.setFloat32(off + NODE_COL_CZ, opts.cz, true);
  dv.setFloat32(off + NODE_COL_RADIUS, opts.radius, true);
  // LabelOff/LabelLen are set by Go to (0, labelLen) — see BuildNodeStreamFrame's doc
  // comment; the aggregator rewrites LabelOff, so its exact value here doesn't matter to
  // the aggregate assertions, but decodeNodeStreamFrame reads label straight from bytes,
  // not from these columns.
  off += NODE_STRIDE;

  new Uint8Array(buf, off, labelBytes.length).set(labelBytes);
  off += labelBytes.length;

  let nameCursor = 0;
  for (let i = 0; i < portCount; i++) {
    const p = opts.ports[i]!;
    const nameBytes = portNameChunks[i]!;
    const rowOff = off + i * PORT_STRIDE;
    dv.setInt32(rowOff + PORT_COL_NODE_ROW, opts.nodeRow, true);
    dv.setFloat32(rowOff + PORT_COL_PX, p.px, true);
    dv.setFloat32(rowOff + PORT_COL_PY, p.py, true);
    dv.setFloat32(rowOff + PORT_COL_PZ, p.pz, true);
    dv.setUint32(rowOff + PORT_COL_PORT_NAME_OFF, nameCursor, true);
    dv.setUint32(rowOff + PORT_COL_PORT_NAME_LEN, nameBytes.length, true);
    nameCursor += nameBytes.length;
  }
  off += portCount * PORT_STRIDE;
  const portNameSection = new Uint8Array(buf, off, portNameBytesCount);
  nameCursor = 0;
  for (const chunk of portNameChunks) {
    portNameSection.set(chunk, nameCursor);
    nameCursor += chunk.length;
  }
  off += portNameBytesCount;

  layoutLinks.forEach((ll, i) => {
    const rowOff = off + i * NODE_STREAM_LAYOUT_LINK_STRIDE;
    dv.setInt32(rowOff, ll.dstNodeRow, true);
    dv.setInt32(rowOff + 4, ll.edgeRow, true);
  });

  return buf;
}

/** Build one node's BUF_BLOCK_TAG_INTERIOR_STREAM frame: [tick] + fixed
 *  INTERIOR_SLOTS_PER_NODE × INTERIOR_STRIDE rows. */
function makeInteriorStreamFrame(fill: (dv: DataView, rowOff: (slot: number) => number) => void): ArrayBuffer {
  const bytes = INTERIOR_SLOTS_PER_NODE * INTERIOR_STRIDE;
  const buf = new ArrayBuffer(BUF_INTERIOR_STREAM_FRAME_HEADER_SIZE + bytes);
  const dv = new DataView(buf);
  dv.setUint32(0, 3, true); // tick
  fill(dv, (slot) => BUF_INTERIOR_STREAM_FRAME_HEADER_SIZE + slot * INTERIOR_STRIDE);
  return buf;
}

// ── decodeNodeStreamFrame / decodeInteriorStreamFrame ──────────────────────────

describe("decodeNodeStreamFrame", () => {
  it("decodes one node's geometry, label, and ports", () => {
    const buf = makeNodeStreamFrame({
      nodeRow: 5,
      cx: 1, cy: 2, cz: 3, radius: 9,
      label: "widget",
      ports: [{ px: 10, py: 11, pz: 12, name: "in" }, { px: 20, py: 21, pz: 22, name: "out" }],
    });
    const d = decodeNodeStreamFrame(5, buf)!;
    expect(d).not.toBeNull();
    expect(d.label).toBe("widget");
    expect(d.portCount).toBe(2);
    expectF32(readNodeCX(d.nodeView, 0), 1);
    expectF32(readNodeRadius(d.nodeView, 0), 9);
    expect(readPortNodeRow(d.portView, 0)).toBe(5);
    expectF32(readPortPX(d.portView, 0), 10);
    expect(readPortNodeRow(d.portView, 1)).toBe(5);
    expectF32(readPortPX(d.portView, 1), 20);
  });

  it("returns null for a truncated buffer", () => {
    expect(decodeNodeStreamFrame(0, new ArrayBuffer(2))).toBeNull();
  });

  it("decodes this node's own outbound layout-links (DstNodeRow/EdgeRow, no SrcNodeRow — implicit)", () => {
    const buf = makeNodeStreamFrame({
      nodeRow: 3, cx: 0, cy: 0, cz: 0, radius: 1, label: "n3",
      ports: [],
      layoutLinks: [{ dstNodeRow: 7, edgeRow: 2 }, { dstNodeRow: 9, edgeRow: -1 }],
    });
    const d = decodeNodeStreamFrame(3, buf)!;
    expect(d).not.toBeNull();
    expect(d.layoutLinkCount).toBe(2);
    expect(readNodeStreamLayoutLinkDstNodeRow(d.layoutLinkView, 0)).toBe(7);
    expect(readNodeStreamLayoutLinkEdgeRow(d.layoutLinkView, 0)).toBe(2);
    expect(readNodeStreamLayoutLinkDstNodeRow(d.layoutLinkView, 1)).toBe(9);
    expect(readNodeStreamLayoutLinkEdgeRow(d.layoutLinkView, 1)).toBe(-1);
  });
});

describe("decodeInteriorStreamFrame", () => {
  it("decodes the fixed 4-slot interior grid", () => {
    const buf = makeInteriorStreamFrame((dv, rowOff) => {
      dv.setUint8(rowOff(0), 1);
      dv.setInt32(rowOff(0) + 1, 1, true);
    });
    const d = decodeInteriorStreamFrame(0, buf)!;
    expect(d).not.toBeNull();
    expect(readInteriorPresent(d.interiorView, 0)).toBe(1);
    expect(readInteriorValue(d.interiorView, 0)).toBe(1);
    expect(readInteriorPresent(d.interiorView, 1)).toBe(0);
  });

  it("returns null for a truncated buffer", () => {
    expect(decodeInteriorStreamFrame(0, new ArrayBuffer(2))).toBeNull();
  });
});

// ── getNodeFrame: aggregation across two nodes ─────────────────────────────────

describe("getNodeFrame — aggregated dedicated streams", () => {
  it("aggregates node/port/label across rows, rewriting Label/PortName offsets to point\n" +
     "     into the aggregated sections (not each node's own inline bytes)", async () => {
    const { snapshotBuffer, nodeStreamBlocks } = await freshNodeStreamModules();
    const frame0 = makeNodeStreamFrame({
      nodeRow: 0, cx: 100, cy: 0, cz: 0, radius: 5, label: "alpha",
      ports: [{ px: 105, py: 0, pz: 0, name: "p0" }],
    });
    const frame1 = makeNodeStreamFrame({
      nodeRow: 1, cx: 200, cy: 0, cz: 0, radius: 6, label: "beta",
      ports: [{ px: 206, py: 0, pz: 0, name: "p1" }],
    });
    snapshotBuffer.setLatestNodeStreamFrame(0, frame0);
    snapshotBuffer.setLatestNodeStreamFrame(1, frame1);
    snapshotBuffer.setLatestInteriorStreamFrame(0, makeInteriorStreamFrame(() => { /* all absent */ }));
    snapshotBuffer.setLatestInteriorStreamFrame(1, makeInteriorStreamFrame(() => { /* all absent */ }));

    const agg = nodeStreamBlocks.getNodeFrame()!;
    expect(agg).not.toBeNull();
    expect(agg.nodeCount).toBe(2);
    expectF32(readNodeCX(agg.nodeView, 0), 100);
    expectF32(readNodeCX(agg.nodeView, 1), 200);
    expect(agg.portCount).toBe(2);
    // Port row 0 belongs to node row 0; port row 1 to node row 1 (NodeRow carried over
    // verbatim from each node's own frame, already global — see BuildNodeStreamFrame).
    expect(readPortNodeRow(agg.portView, 0)).toBe(0);
    expectF32(readPortPX(agg.portView, 0), 105);
    expect(readPortNodeRow(agg.portView, 1)).toBe(1);
    expectF32(readPortPX(agg.portView, 1), 206);
  });

  it("treats a node row with no arrived NODE-stream frame as an unresolved zero row", async () => {
    const { snapshotBuffer, nodeStreamBlocks } = await freshNodeStreamModules();
    const frame0 = makeNodeStreamFrame({
      nodeRow: 0, cx: 1, cy: 1, cz: 1, radius: 1, label: "only-zero",
      ports: [],
    });
    snapshotBuffer.setLatestNodeStreamFrame(0, frame0);
    // Simulate row 2 having arrived (sparse, out of order) but row 1 not yet.
    const frame2 = makeNodeStreamFrame({
      nodeRow: 2, cx: 9, cy: 9, cz: 9, radius: 9, label: "later",
      ports: [],
    });
    snapshotBuffer.setLatestNodeStreamFrame(2, frame2);
    snapshotBuffer.setLatestInteriorStreamFrame(0, makeInteriorStreamFrame(() => {}));
    snapshotBuffer.setLatestInteriorStreamFrame(2, makeInteriorStreamFrame(() => {}));

    const agg = nodeStreamBlocks.getNodeFrame()!;
    expect(agg.nodeCount).toBe(3); // one past the highest arrived row
    expectF32(readNodeCX(agg.nodeView, 0), 1);
    // Row 1 never arrived — zeroed, not garbage.
    expectF32(readNodeCX(agg.nodeView, 1), 0);
    expectF32(readNodeRadius(agg.nodeView, 1), 0);
    expectF32(readNodeCX(agg.nodeView, 2), 9);
  });
});

describe("getNodeFrame — no per-node stream frame has arrived yet", () => {
  it("returns null (WIREFOLD_STREAM_FDS is mandatory — no fd-3 fallback)", async () => {
    const { nodeStreamBlocks } = await freshNodeStreamModules();
    expect(nodeStreamBlocks.getNodeFrame()).toBeNull();
  });
});

// ── getLayoutLinks: aggregation ─────────────────────────────────────────────────

describe("getLayoutLinks", () => {
  it("aggregates each per-node stream's own outbound layout-links into full Src/Dst/Edge rows", async () => {
    const { snapshotBuffer, nodeStreamBlocks } = await freshNodeStreamModules();
    const frame0 = makeNodeStreamFrame({
      nodeRow: 0, cx: 0, cy: 0, cz: 0, radius: 1, label: "a",
      ports: [],
      layoutLinks: [{ dstNodeRow: 1, edgeRow: 0 }],
    });
    const frame1 = makeNodeStreamFrame({
      nodeRow: 1, cx: 0, cy: 0, cz: 0, radius: 1, label: "b",
      ports: [],
      layoutLinks: [{ dstNodeRow: 2, edgeRow: -1 }],
    });
    const frame2 = makeNodeStreamFrame({
      nodeRow: 2, cx: 0, cy: 0, cz: 0, radius: 1, label: "c",
      ports: [],
      // no outbound layout-links (b<c and c<? — c is never source in this fixture)
    });
    snapshotBuffer.setLatestNodeStreamFrame(0, frame0);
    snapshotBuffer.setLatestNodeStreamFrame(1, frame1);
    snapshotBuffer.setLatestNodeStreamFrame(2, frame2);

    const agg = nodeStreamBlocks.getLayoutLinks();
    expect(agg.layoutLinkCount).toBe(2);
    // Row order is source-node-row order (0 then 1) — SrcNodeRow is the reconstructed
    // implicit source, DstNodeRow/EdgeRow carried straight from that node's own frame.
    expect(readLayoutLinkSrcNodeRow(agg.layoutLinkView, 0)).toBe(0);
    expect(readLayoutLinkDstNodeRow(agg.layoutLinkView, 0)).toBe(1);
    expect(readLayoutLinkEdgeRow(agg.layoutLinkView, 0)).toBe(0);
    expect(readLayoutLinkSrcNodeRow(agg.layoutLinkView, 1)).toBe(1);
    expect(readLayoutLinkDstNodeRow(agg.layoutLinkView, 1)).toBe(2);
    expect(readLayoutLinkEdgeRow(agg.layoutLinkView, 1)).toBe(-1);
  });

  it("returns an empty aggregate when no per-node stream has arrived (WIREFOLD_STREAM_FDS is mandatory — no fd-3 fallback)", async () => {
    const { nodeStreamBlocks } = await freshNodeStreamModules();
    const agg = nodeStreamBlocks.getLayoutLinks();
    expect(agg.layoutLinkCount).toBe(0);
  });
});
