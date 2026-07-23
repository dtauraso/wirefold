// Unit tests for buffer-decode.ts — pure decodeBeadFrame + decodeNodeFrame + decodeEdgeFrame
// functions.
//
// Builds raw ArrayBuffers matching the Go bead-frame / node-frame / edge-frame layouts
// (little-endian, packed) and asserts that decodeBeadFrame/decodeNodeFrame/decodeEdgeFrame
// produce the correct counts and per-row values via the buffer-layout.ts read helpers.

import { describe, it, expect } from "vitest";
import { decodeBeadFrame, decodeNodeFrame, decodeEdgeFrame, nodeLabel, INTERIOR_SLOTS_PER_NODE } from "../src/webview/three/buffer-decode";
import {
  BEAD_STRIDE, NODE_STRIDE, INTERIOR_STRIDE, EDGE_STRIDE, PORT_STRIDE,
  PORT_COL_NODE_ROW, PORT_COL_DX, PORT_COL_DY, PORT_COL_DZ, PORT_COL_IS_INPUT,
  NODE_COL_LABEL_OFF, NODE_COL_LABEL_LEN,
  EDGE_COL_SRC_PORT_ROW, EDGE_COL_DST_PORT_ROW,
  readBeadX, readBeadY, readBeadZ, readBeadLive,
  readNodeCX, readNodeCY, readNodeCZ, readNodeRadius,
  readInteriorPresent, readInteriorValue, readInteriorOX, readInteriorOY, readInteriorOZ,
  readEdgeSrcPortRow, readEdgeDstPortRow,
  readPortNodeRow, readPortDX, readPortDY, readPortDZ, readPortIsInput,
} from "../src/schema/buffer-layout";
import { BUF_BEAD_HEADER_SIZE, BUF_NODE_FRAME_HEADER_SIZE, BUF_EDGE_FRAME_HEADER_SIZE } from "../src/schema/frame-tags";

// ── helpers ───────────────────────────────────────────────────────────────────

function expectF32(got: number, want: number) {
  expect(got).toBeCloseTo(want, 5);
}

/**
 * Build a minimal EDGE frame ArrayBuffer (BUF_BLOCK_TAG_EDGE) for a given edge count:
 * [tick:u32][edgeCount:u32][edgeLabelBytesCount:u32] followed by edgeCount × EDGE_STRIDE
 * rows. Caller receives a DataView over the entire buffer so it can fill in fields.
 */
function makeEdgeFrame(edgeCount: number): { buf: ArrayBuffer; dv: DataView; edgeOff: number } {
  const edgeBytes = edgeCount * EDGE_STRIDE;
  const buf = new ArrayBuffer(BUF_EDGE_FRAME_HEADER_SIZE + edgeBytes);
  const dv = new DataView(buf);
  dv.setUint32(0, 0, true); // tick
  dv.setUint32(4, edgeCount, true); // edgeCount
  return { buf, dv, edgeOff: BUF_EDGE_FRAME_HEADER_SIZE };
}

/**
 * Build a minimal NODE frame ArrayBuffer (BUF_BLOCK_TAG_NODE) for given node/port counts:
 * [tick:u32][nodeCount:u32][portCount:u32][labelBytesCount:u32][portNameBytesCount:u32]
 * followed by the Node block, the fixed Interior block, and the Port block. Caller
 * receives a DataView over the entire buffer so it can fill in fields.
 */
function makeNodeFrame(nodeCount: number, portCount = 0): {
  buf: ArrayBuffer;
  dv: DataView;
  nodeOff: number;
  interiorOff: number;
  portOff: number;
} {
  const nodeBytes     = nodeCount * NODE_STRIDE;
  const interiorBytes = nodeCount * INTERIOR_SLOTS_PER_NODE * INTERIOR_STRIDE;
  const portBytes     = portCount * PORT_STRIDE;
  const totalBytes = BUF_NODE_FRAME_HEADER_SIZE + nodeBytes + interiorBytes + portBytes;

  const buf = new ArrayBuffer(totalBytes);
  const dv  = new DataView(buf);

  dv.setUint32(0,  0,         true); // tick
  dv.setUint32(4,  nodeCount, true);
  dv.setUint32(8,  portCount, true);

  const nodeOff     = BUF_NODE_FRAME_HEADER_SIZE;
  const interiorOff = nodeOff     + nodeBytes;
  const portOff     = interiorOff + interiorBytes;

  return { buf, dv, nodeOff, interiorOff, portOff };
}

/** Build a minimal BUF_BLOCK_TAG_BEAD frame ArrayBuffer for a given bead count: [tick:u32]
 *  [beadCount:u32] followed by beadCount × BEAD_STRIDE rows. */
function makeBeadFrame(beadCount: number): { buf: ArrayBuffer; dv: DataView; beadOff: number } {
  const beadBytes = beadCount * BEAD_STRIDE;
  const buf = new ArrayBuffer(BUF_BEAD_HEADER_SIZE + beadBytes);
  const dv = new DataView(buf);
  dv.setUint32(0, 0, true); // tick
  dv.setUint32(4, beadCount, true);
  return { buf, dv, beadOff: BUF_BEAD_HEADER_SIZE };
}

// ── tests ─────────────────────────────────────────────────────────────────────

describe("decodeNodeFrame — null for bad input", () => {
  it("returns null for empty buffer", () => {
    expect(decodeNodeFrame(new ArrayBuffer(0))).toBeNull();
  });

  it("returns null for header-only buffer (truncated body)", () => {
    // Header says 1 node but body is missing.
    const buf = new ArrayBuffer(BUF_NODE_FRAME_HEADER_SIZE);
    const dv  = new DataView(buf);
    dv.setUint32(4, 1, true); // nodeCount=1 but no body
    expect(decodeNodeFrame(buf)).toBeNull();
  });
});

describe("decodeNodeFrame — empty frame (no nodes)", () => {
  it("decodes header counts as 0", () => {
    const { buf } = makeNodeFrame(0);
    const d = decodeNodeFrame(buf);
    expect(d).not.toBeNull();
    expect(d!.nodeCount).toBe(0);
    expect(d!.tick).toBe(0);
  });

  it("block views have zero byteLength for empty blocks", () => {
    const { buf } = makeNodeFrame(0);
    const d = decodeNodeFrame(buf)!;
    expect(d.nodeView.byteLength).toBe(0);
    expect(d.interiorView.byteLength).toBe(0);
    expect(d.portView.byteLength).toBe(0);
  });
});

describe("decodeBeadFrame — null for bad input", () => {
  it("returns null for empty buffer", () => {
    expect(decodeBeadFrame(new ArrayBuffer(0))).toBeNull();
  });

  it("returns null for header-only buffer (truncated body)", () => {
    // Header says 1 bead but body is missing.
    const buf = new ArrayBuffer(BUF_BEAD_HEADER_SIZE);
    const dv  = new DataView(buf);
    dv.setUint32(4, 1, true); // beadCount=1 but no body
    expect(decodeBeadFrame(buf)).toBeNull();
  });
});

describe("decodeBeadFrame — bead block", () => {
  it("decodes two bead rows correctly", () => {
    const { buf, dv, beadOff } = makeBeadFrame(2);

    // Row 0: live bead at (1, 2, 3)
    dv.setFloat32(beadOff + 0 * BEAD_STRIDE + 0,  1.0, true); // X
    dv.setFloat32(beadOff + 0 * BEAD_STRIDE + 4,  2.0, true); // Y
    dv.setFloat32(beadOff + 0 * BEAD_STRIDE + 8,  3.0, true); // Z
    dv.setUint8(  beadOff + 0 * BEAD_STRIDE + 16, 1);         // Live=1

    // Row 1: dead bead at (10, 20, 30)
    dv.setFloat32(beadOff + 1 * BEAD_STRIDE + 0,  10.0, true);
    dv.setFloat32(beadOff + 1 * BEAD_STRIDE + 4,  20.0, true);
    dv.setFloat32(beadOff + 1 * BEAD_STRIDE + 8,  30.0, true);
    dv.setUint8(  beadOff + 1 * BEAD_STRIDE + 16, 0);          // Live=0

    const d = decodeBeadFrame(buf)!;
    expect(d.beadCount).toBe(2);
    expect(d.beadView.byteLength).toBe(2 * BEAD_STRIDE);

    const bv = d.beadView;
    expectF32(readBeadX(bv, 0), 1.0);
    expectF32(readBeadY(bv, 0), 2.0);
    expectF32(readBeadZ(bv, 0), 3.0);
    expect(readBeadLive(bv, 0)).toBe(1);

    expectF32(readBeadX(bv, 1), 10.0);
    expectF32(readBeadY(bv, 1), 20.0);
    expectF32(readBeadZ(bv, 1), 30.0);
    expect(readBeadLive(bv, 1)).toBe(0);
  });
});

describe("decodeNodeFrame — node block", () => {
  it("decodes a single node row correctly", () => {
    const { buf, dv, nodeOff } = makeNodeFrame(1);

    // Node 0: center (5, 6, 7), radius=15
    dv.setFloat32(nodeOff + 0 * NODE_STRIDE + 0,  5.0,  true); // CX
    dv.setFloat32(nodeOff + 0 * NODE_STRIDE + 4,  6.0,  true); // CY
    dv.setFloat32(nodeOff + 0 * NODE_STRIDE + 8,  7.0,  true); // CZ
    dv.setFloat32(nodeOff + 0 * NODE_STRIDE + 12, 15.0, true); // Radius

    const d = decodeNodeFrame(buf)!;
    expect(d.nodeCount).toBe(1);

    const nv = d.nodeView;
    expectF32(readNodeCX(nv, 0), 5.0);
    expectF32(readNodeCY(nv, 0), 6.0);
    expectF32(readNodeCZ(nv, 0), 7.0);
    expectF32(readNodeRadius(nv, 0), 15.0);
  });
});

describe("decodeNodeFrame — interior block", () => {
  it("slices the interior block after the node block and decodes slot rows", () => {
    // 1 node → INTERIOR_SLOTS_PER_NODE interior rows. Fill node 0's slot 0 with a
    // present bead value=1 at local offset (2,-3,0), and slot 2 with value=0 at (0,4,0).
    const { buf, dv, interiorOff } = makeNodeFrame(1);
    const row0 = interiorOff + 0 * INTERIOR_STRIDE;
    dv.setUint8(row0 + 0, 1);            // Present
    dv.setInt32(row0 + 1, 1, true);      // Value
    dv.setFloat32(row0 + 5, 2.0, true);  // OX
    dv.setFloat32(row0 + 9, -3.0, true); // OY
    dv.setFloat32(row0 + 13, 0.0, true); // OZ

    const row2 = interiorOff + 2 * INTERIOR_STRIDE;
    dv.setUint8(row2 + 0, 1);            // Present
    dv.setInt32(row2 + 1, 0, true);      // Value=0 (valid black bead)
    dv.setFloat32(row2 + 9, 4.0, true);  // OY

    const d = decodeNodeFrame(buf)!;
    expect(d.interiorCount).toBe(INTERIOR_SLOTS_PER_NODE);
    const iv = d.interiorView;

    expect(readInteriorPresent(iv, 0)).toBe(1);
    expect(readInteriorValue(iv, 0)).toBe(1);
    expectF32(readInteriorOX(iv, 0), 2.0);
    expectF32(readInteriorOY(iv, 0), -3.0);
    expectF32(readInteriorOZ(iv, 0), 0.0);

    // Slot 1 untouched → absent.
    expect(readInteriorPresent(iv, 1)).toBe(0);

    // Slot 2: present, value 0.
    expect(readInteriorPresent(iv, 2)).toBe(1);
    expect(readInteriorValue(iv, 2)).toBe(0);
    expectF32(readInteriorOY(iv, 2), 4.0);
  });
});

describe("decodeEdgeFrame — edge block", () => {
  it("returns null for empty buffer", () => {
    expect(decodeEdgeFrame(new ArrayBuffer(0))).toBeNull();
  });

  it("decodes a single edge row correctly — SrcPortRow/DstPortRow reference port rows,\n" +
     "     no endpoint coordinate is stored on the edge itself (endpoint-tear fix)", () => {
    const { buf, dv, edgeOff } = makeEdgeFrame(1);

    // Edge 0: references port row 3 (source) and port row 7 (dest) — the ONLY place the
    // endpoint's world position lives is the NODE frame's Port block (see EdgeTube.tsx).
    dv.setInt32(edgeOff + EDGE_COL_SRC_PORT_ROW, 3, true);
    dv.setInt32(edgeOff + EDGE_COL_DST_PORT_ROW, 7, true);

    const d = decodeEdgeFrame(buf)!;
    expect(d.edgeCount).toBe(1);

    const ev = d.edgeView;
    expect(readEdgeSrcPortRow(ev, 0)).toBe(3);
    expect(readEdgeDstPortRow(ev, 0)).toBe(7);
  });
});

describe("live-bead instance-count logic", () => {
  it("counts only live=1 bead rows, matching BeadInstances slot-fill logic", () => {
    // 3 beads: rows 0 and 2 live, row 1 dead. Mirrors the filter in BeadInstances.useFrame.
    const { buf, dv, beadOff } = makeBeadFrame(3);
    dv.setUint8(beadOff + 0 * BEAD_STRIDE + 16, 1); // Live=1
    dv.setUint8(beadOff + 1 * BEAD_STRIDE + 16, 0); // Live=0 (dead)
    dv.setUint8(beadOff + 2 * BEAD_STRIDE + 16, 1); // Live=1

    const d = decodeBeadFrame(buf)!;
    expect(d.beadCount).toBe(3); // header count is total rows (live + dead)

    let liveSlot = 0;
    for (let i = 0; i < d.beadCount; i++) {
      if (readBeadLive(d.beadView, i)) liveSlot++;
    }
    // Only 2 live beads → only 2 instances should be drawn, not 3
    expect(liveSlot).toBe(2);
  });

  it("all-dead beads yield zero live slots", () => {
    const { buf, dv, beadOff } = makeBeadFrame(2);
    dv.setUint8(beadOff + 0 * BEAD_STRIDE + 16, 0);
    dv.setUint8(beadOff + 1 * BEAD_STRIDE + 16, 0);

    const d = decodeBeadFrame(buf)!;
    let liveSlot = 0;
    for (let i = 0; i < d.beadCount; i++) {
      if (readBeadLive(d.beadView, i)) liveSlot++;
    }
    expect(liveSlot).toBe(0);
  });
});

describe("decodeNodeFrame — port block", () => {
  it("slices the port block after the interior block; instanceId row == port row", () => {
    // 2 nodes, 3 ports. Rows: (nodeRow 0, in, dir -1,0,0), (nodeRow 0, out, dir 1,0,0),
    // (nodeRow 1, in, dir 0,1,0). Mirrors the flattened Go Port block order.
    const { buf, dv, portOff } = makeNodeFrame(2, 3);

    const setPort = (row: number, nodeRow: number, dx: number, dy: number, dz: number, isInput: number) => {
      const o = portOff + row * PORT_STRIDE;
      dv.setInt32(o + PORT_COL_NODE_ROW, nodeRow, true);
      dv.setFloat32(o + PORT_COL_DX, dx, true);
      dv.setFloat32(o + PORT_COL_DY, dy, true);
      dv.setFloat32(o + PORT_COL_DZ, dz, true);
      dv.setUint8(o + PORT_COL_IS_INPUT, isInput);
    };
    setPort(0, 0, -1, 0, 0, 1);
    setPort(1, 0, 1, 0, 0, 0);
    setPort(2, 1, 0, 1, 0, 1);

    const d = decodeNodeFrame(buf)!;
    expect(d.portCount).toBe(3);
    expect(d.portView.byteLength).toBe(3 * PORT_STRIDE);

    const pv = d.portView;
    // Row 0 (the same index a port InstancedMesh instanceId carries).
    expect(readPortNodeRow(pv, 0)).toBe(0);
    expectF32(readPortDX(pv, 0), -1);
    expect(readPortIsInput(pv, 0)).toBe(1);
    // Row 1.
    expect(readPortNodeRow(pv, 1)).toBe(0);
    expectF32(readPortDX(pv, 1), 1);
    expect(readPortIsInput(pv, 1)).toBe(0);
    // Row 2 — a different owning node row.
    expect(readPortNodeRow(pv, 2)).toBe(1);
    expectF32(readPortDY(pv, 2), 1);
    expect(readPortIsInput(pv, 2)).toBe(1);
  });
});

describe("decodeNodeFrame — label section", () => {
  it("decodes the labelBytesCount header field and slices each node's label", () => {
    // 2 nodes with labels "alpha" and "β-node" (β is 2 UTF-8 bytes → 7-byte label).
    const enc = new TextEncoder();
    const labels = [enc.encode("alpha"), enc.encode("β-node")];
    const labelBytesCount = labels[0]!.length + labels[1]!.length; // 5 + 7 = 12
    const nodeBytes = 2 * NODE_STRIDE;
    const interiorBytes = 2 * INTERIOR_SLOTS_PER_NODE * INTERIOR_STRIDE;
    const total = BUF_NODE_FRAME_HEADER_SIZE + nodeBytes + interiorBytes + labelBytesCount;
    const buf = new ArrayBuffer(total);
    const dv = new DataView(buf);
    dv.setUint32(4, 2, true);                // nodeCount
    dv.setUint32(12, labelBytesCount, true); // labelBytesCount
    const nodeOff = BUF_NODE_FRAME_HEADER_SIZE;
    const labelSecOff = BUF_NODE_FRAME_HEADER_SIZE + nodeBytes + interiorBytes;
    const labelView = new Uint8Array(buf, labelSecOff, labelBytesCount);
    let cursor = 0;
    labels.forEach((chunk, row) => {
      const base = nodeOff + row * NODE_STRIDE;
      dv.setUint32(base + NODE_COL_LABEL_OFF, cursor, true);
      dv.setUint32(base + NODE_COL_LABEL_LEN, chunk.length, true);
      labelView.set(chunk, cursor);
      cursor += chunk.length;
    });

    const d = decodeNodeFrame(buf)!;
    expect(d.labelBytesCount).toBe(12);
    expect(d.labelBytes.byteLength).toBe(12);
    expect(nodeLabel(d, 0)).toBe("alpha");
    expect(nodeLabel(d, 1)).toBe("β-node");
  });

  it("decodes an unset label (len 0) as the empty string", () => {
    const nodeBytes = 1 * NODE_STRIDE;
    const interiorBytes = 1 * INTERIOR_SLOTS_PER_NODE * INTERIOR_STRIDE;
    const total = BUF_NODE_FRAME_HEADER_SIZE + nodeBytes + interiorBytes;
    const buf = new ArrayBuffer(total);
    new DataView(buf).setUint32(4, 1, true); // nodeCount=1, labelBytesCount=0
    const d = decodeNodeFrame(buf)!;
    expect(d.labelBytesCount).toBe(0);
    expect(nodeLabel(d, 0)).toBe("");
  });
});

describe("decodeNodeFrame / decodeEdgeFrame — mixed counts", () => {
  it("correctly slices views when nodes and edges are present", () => {
    const { buf: nodeBuf } = makeNodeFrame(2);
    const { buf: edgeBuf } = makeEdgeFrame(4);
    const dn = decodeNodeFrame(nodeBuf)!;
    const de = decodeEdgeFrame(edgeBuf)!;
    expect(dn.nodeCount).toBe(2);
    expect(de.edgeCount).toBe(4);
    expect(dn.nodeView.byteLength).toBe(2 * NODE_STRIDE);
    expect(dn.interiorCount).toBe(2 * INTERIOR_SLOTS_PER_NODE);
    expect(dn.interiorView.byteLength).toBe(2 * INTERIOR_SLOTS_PER_NODE * INTERIOR_STRIDE);
    expect(de.edgeView.byteLength).toBe(4 * EDGE_STRIDE);
  });

  it("views alias the original buffer (no copy)", () => {
    const { buf, dv, beadOff } = makeBeadFrame(1);
    // Write a sentinel value before decoding.
    dv.setFloat32(beadOff + 0, 99.5, true); // Bead[0].X = 99.5

    const d = decodeBeadFrame(buf)!;
    // Mutate through the view — should be visible via readBeadX.
    d.beadView.setFloat32(0, 42.0, true);

    // The underlying buf should now reflect the mutation.
    expectF32(new DataView(buf).getFloat32(beadOff, true), 42.0);
  });
});
