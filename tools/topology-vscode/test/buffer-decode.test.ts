// Unit tests for buffer-decode.ts — pure decodeSnapshot function.
//
// Builds raw ArrayBuffers matching the Go snapshot layout (little-endian, packed)
// and asserts that decodeSnapshot produces the correct counts and per-row values
// via the buffer-layout.ts read helpers.

import { describe, it, expect } from "vitest";
import { decodeSnapshot } from "../src/webview/three/buffer-decode";
import {
  BUF_HEADER_SIZE,
  BEAD_STRIDE, NODE_STRIDE, EDGE_STRIDE, CAMERA_STRIDE, OVERLAY_STRIDE,
  readBeadX, readBeadY, readBeadZ, readBeadFrac, readBeadLive, readBeadBeadID,
  readNodeCX, readNodeCY, readNodeCZ, readNodeRadius,
  readEdgeSX, readEdgeSY, readEdgeSZ, readEdgeEX, readEdgeEY, readEdgeEZ,
} from "../src/schema/buffer-layout";

// ── helpers ───────────────────────────────────────────────────────────────────

function expectF32(got: number, want: number) {
  expect(got).toBeCloseTo(want, 5);
}

/**
 * Build a minimal snapshot ArrayBuffer for given counts.
 * Caller receives a DataView over the entire buffer so it can fill in fields.
 */
function makeSnapshot(beadCount: number, nodeCount: number, edgeCount: number): {
  buf: ArrayBuffer;
  dv: DataView;
  beadOff: number;
  nodeOff: number;
  edgeOff: number;
  cameraOff: number;
  overlayOff: number;
} {
  const beadBytes   = beadCount  * BEAD_STRIDE;
  const nodeBytes   = nodeCount  * NODE_STRIDE;
  const edgeBytes   = edgeCount  * EDGE_STRIDE;
  const totalBytes  = BUF_HEADER_SIZE + beadBytes + nodeBytes + edgeBytes + CAMERA_STRIDE + OVERLAY_STRIDE;

  const buf = new ArrayBuffer(totalBytes);
  const dv  = new DataView(buf);

  // Write header: [tick=0][beadCount][nodeCount][edgeCount]
  dv.setUint32(0,  0,          true); // tick
  dv.setUint32(4,  beadCount,  true);
  dv.setUint32(8,  nodeCount,  true);
  dv.setUint32(12, edgeCount,  true);

  const beadOff   = BUF_HEADER_SIZE;
  const nodeOff   = beadOff  + beadBytes;
  const edgeOff   = nodeOff  + nodeBytes;
  const cameraOff = edgeOff  + edgeBytes;
  const overlayOff = cameraOff + CAMERA_STRIDE;

  return { buf, dv, beadOff, nodeOff, edgeOff, cameraOff, overlayOff };
}

// ── tests ─────────────────────────────────────────────────────────────────────

describe("decodeSnapshot — null for bad input", () => {
  it("returns null for empty buffer", () => {
    expect(decodeSnapshot(new ArrayBuffer(0))).toBeNull();
  });

  it("returns null for header-only buffer (truncated body)", () => {
    // Header says 1 bead but body is missing.
    const buf = new ArrayBuffer(BUF_HEADER_SIZE);
    const dv  = new DataView(buf);
    dv.setUint32(4, 1, true); // beadCount=1 but no body
    expect(decodeSnapshot(buf)).toBeNull();
  });

  it("returns null when buffer is one byte short of expected size", () => {
    // 0 beads/nodes/edges → expected = header + camera + overlay
    const expected = BUF_HEADER_SIZE + CAMERA_STRIDE + OVERLAY_STRIDE;
    const buf = new ArrayBuffer(expected - 1);
    expect(decodeSnapshot(buf)).toBeNull();
  });
});

describe("decodeSnapshot — empty snapshot (no beads, nodes, edges)", () => {
  it("decodes header counts as 0", () => {
    const { buf } = makeSnapshot(0, 0, 0);
    const d = decodeSnapshot(buf);
    expect(d).not.toBeNull();
    expect(d!.beadCount).toBe(0);
    expect(d!.nodeCount).toBe(0);
    expect(d!.edgeCount).toBe(0);
    expect(d!.tick).toBe(0);
  });

  it("block views have zero byteLength for empty blocks", () => {
    const { buf } = makeSnapshot(0, 0, 0);
    const d = decodeSnapshot(buf)!;
    expect(d.beadView.byteLength).toBe(0);
    expect(d.nodeView.byteLength).toBe(0);
    expect(d.edgeView.byteLength).toBe(0);
    expect(d.cameraView.byteLength).toBe(CAMERA_STRIDE);
    expect(d.overlayView.byteLength).toBe(OVERLAY_STRIDE);
  });
});

describe("decodeSnapshot — bead block", () => {
  it("decodes two bead rows correctly", () => {
    const { buf, dv, beadOff } = makeSnapshot(2, 0, 0);

    // Row 0: live bead at (1, 2, 3), frac=0.5, beadID=7
    dv.setFloat32(beadOff + 0 * BEAD_STRIDE + 0,  1.0, true); // X
    dv.setFloat32(beadOff + 0 * BEAD_STRIDE + 4,  2.0, true); // Y
    dv.setFloat32(beadOff + 0 * BEAD_STRIDE + 8,  3.0, true); // Z
    dv.setFloat32(beadOff + 0 * BEAD_STRIDE + 16, 0.5, true); // Frac
    dv.setUint32( beadOff + 0 * BEAD_STRIDE + 20, 7,   true); // BeadID
    dv.setUint8(  beadOff + 0 * BEAD_STRIDE + 24, 1);         // Live=1

    // Row 1: dead bead at (10, 20, 30), frac=0.0, beadID=8
    dv.setFloat32(beadOff + 1 * BEAD_STRIDE + 0,  10.0, true);
    dv.setFloat32(beadOff + 1 * BEAD_STRIDE + 4,  20.0, true);
    dv.setFloat32(beadOff + 1 * BEAD_STRIDE + 8,  30.0, true);
    dv.setUint8(  beadOff + 1 * BEAD_STRIDE + 24, 0);          // Live=0

    const d = decodeSnapshot(buf)!;
    expect(d.beadCount).toBe(2);
    expect(d.beadView.byteLength).toBe(2 * BEAD_STRIDE);

    const bv = d.beadView;
    expectF32(readBeadX(bv, 0), 1.0);
    expectF32(readBeadY(bv, 0), 2.0);
    expectF32(readBeadZ(bv, 0), 3.0);
    expectF32(readBeadFrac(bv, 0), 0.5);
    expect(readBeadBeadID(bv, 0)).toBe(7);
    expect(readBeadLive(bv, 0)).toBe(1);

    expectF32(readBeadX(bv, 1), 10.0);
    expectF32(readBeadY(bv, 1), 20.0);
    expectF32(readBeadZ(bv, 1), 30.0);
    expect(readBeadLive(bv, 1)).toBe(0);
  });
});

describe("decodeSnapshot — node block", () => {
  it("decodes a single node row correctly", () => {
    const { buf, dv, nodeOff } = makeSnapshot(0, 1, 0);

    // Node 0: center (5, 6, 7), radius=15
    dv.setFloat32(nodeOff + 0 * NODE_STRIDE + 0,  5.0,  true); // CX
    dv.setFloat32(nodeOff + 0 * NODE_STRIDE + 4,  6.0,  true); // CY
    dv.setFloat32(nodeOff + 0 * NODE_STRIDE + 8,  7.0,  true); // CZ
    dv.setFloat32(nodeOff + 0 * NODE_STRIDE + 12, 15.0, true); // Radius

    const d = decodeSnapshot(buf)!;
    expect(d.nodeCount).toBe(1);

    const nv = d.nodeView;
    expectF32(readNodeCX(nv, 0), 5.0);
    expectF32(readNodeCY(nv, 0), 6.0);
    expectF32(readNodeCZ(nv, 0), 7.0);
    expectF32(readNodeRadius(nv, 0), 15.0);
  });
});

describe("decodeSnapshot — edge block", () => {
  it("decodes a single edge row correctly", () => {
    const { buf, dv, edgeOff } = makeSnapshot(0, 0, 1);

    // Edge 0: start (1,2,3) → end (4,5,6)
    dv.setFloat32(edgeOff + 0,  1.0, true); // SX
    dv.setFloat32(edgeOff + 4,  2.0, true); // SY
    dv.setFloat32(edgeOff + 8,  3.0, true); // SZ
    dv.setFloat32(edgeOff + 12, 4.0, true); // EX
    dv.setFloat32(edgeOff + 16, 5.0, true); // EY
    dv.setFloat32(edgeOff + 20, 6.0, true); // EZ

    const d = decodeSnapshot(buf)!;
    expect(d.edgeCount).toBe(1);

    const ev = d.edgeView;
    expectF32(readEdgeSX(ev, 0), 1.0);
    expectF32(readEdgeSY(ev, 0), 2.0);
    expectF32(readEdgeSZ(ev, 0), 3.0);
    expectF32(readEdgeEX(ev, 0), 4.0);
    expectF32(readEdgeEY(ev, 0), 5.0);
    expectF32(readEdgeEZ(ev, 0), 6.0);
  });
});

describe("decodeSnapshot — mixed counts", () => {
  it("correctly slices views when beads, nodes, and edges all present", () => {
    const { buf } = makeSnapshot(3, 2, 4);
    const d = decodeSnapshot(buf)!;
    expect(d.beadCount).toBe(3);
    expect(d.nodeCount).toBe(2);
    expect(d.edgeCount).toBe(4);
    expect(d.beadView.byteLength).toBe(3 * BEAD_STRIDE);
    expect(d.nodeView.byteLength).toBe(2 * NODE_STRIDE);
    expect(d.edgeView.byteLength).toBe(4 * EDGE_STRIDE);
    expect(d.cameraView.byteLength).toBe(CAMERA_STRIDE);
    expect(d.overlayView.byteLength).toBe(OVERLAY_STRIDE);
  });

  it("views alias the original buffer (no copy)", () => {
    const { buf, dv, beadOff } = makeSnapshot(1, 0, 0);
    // Write a sentinel value before decoding.
    dv.setFloat32(beadOff + 0, 99.5, true); // Bead[0].X = 99.5

    const d = decodeSnapshot(buf)!;
    // Mutate through the view — should be visible via readBeadX.
    d.beadView.setFloat32(0, 42.0, true);

    // The underlying buf should now reflect the mutation.
    expectF32(new DataView(buf).getFloat32(beadOff, true), 42.0);
  });
});
