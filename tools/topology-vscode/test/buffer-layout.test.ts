// Unit tests for src/schema/buffer-layout.ts — TypedArray reader round-trips.
//
// Each test builds a raw ArrayBuffer of known bytes (little-endian) and asserts
// that the generated read* helpers decode the correct numbers. This exercises the
// column offset math (offset, stride, DataView getter choice) for every block.

import { describe, expect, it } from "vitest";
import {
  BUF_LAYOUT_VERSION,
  BUF_HEADER_SIZE,
  // Bead
  BEAD_COL_X, BEAD_COL_Y, BEAD_COL_Z, BEAD_COL_VALUE, BEAD_COL_FRAC,
  BEAD_COL_BEAD_ID, BEAD_COL_LIVE, BEAD_STRIDE,
  readBeadX, readBeadY, readBeadZ, readBeadValue, readBeadFrac,
  readBeadBeadID, readBeadLive,
  // Node
  NODE_COL_CX, NODE_COL_CY, NODE_COL_CZ, NODE_COL_RADIUS, NODE_COL_SPHERE_R,
  NODE_COL_TORUS_RED, NODE_COL_MISS_VAL, NODE_COL_MX, NODE_COL_MY, NODE_COL_MZ,
  NODE_COL_EV_RECV, NODE_COL_EV_FIRE, NODE_COL_EV_SEND, NODE_COL_EV_ARRIVE, NODE_COL_EV_DONE,
  NODE_COL_SELECTED,
  NODE_STRIDE,
  readNodeCX, readNodeCY, readNodeCZ, readNodeRadius, readNodeSphereR,
  readNodeTorusRed, readNodeMissVal, readNodeMX, readNodeMY, readNodeMZ,
  readNodeEvRecv, readNodeEvFire, readNodeEvSend, readNodeEvArrive, readNodeEvDone,
  readNodeSelected,
  // Edge
  EDGE_COL_SX, EDGE_COL_SY, EDGE_COL_SZ, EDGE_COL_EX, EDGE_COL_EY, EDGE_COL_EZ,
  EDGE_STRIDE,
  readEdgeSX, readEdgeSY, readEdgeSZ, readEdgeEX, readEdgeEY, readEdgeEZ,
  // Camera
  CAMERA_COL_PX, CAMERA_COL_PY, CAMERA_COL_PZ, CAMERA_COL_R,
  CAMERA_COL_POS_THETA, CAMERA_COL_POS_PHI, CAMERA_COL_UP_THETA, CAMERA_COL_UP_PHI,
  CAMERA_STRIDE,
  readCameraPX, readCameraPY, readCameraPZ, readCameraR,
  readCameraPosTheta, readCameraPosPhi, readCameraUpTheta, readCameraUpPhi,
  // Overlay
  OVERLAY_COL_SCENE_TORI, OVERLAY_COL_SCENE_POLES, OVERLAY_COL_NODE_POLES,
  OVERLAY_COL_ANGLE_LABELS, OVERLAY_COL_SEL_SPHERE_POLES, OVERLAY_COL_HANDHOLDS,
  OVERLAY_COL_LABELS_GLOBAL, OVERLAY_COL_BADGES_GLOBAL, OVERLAY_COL_OVERLAYS_VIS,
  OVERLAY_COL_DOUBLE_LINKS, OVERLAY_STRIDE,
  readOverlaySceneTori, readOverlayScenePoles, readOverlayNodePoles,
  readOverlayAngleLabels, readOverlaySelSpherePoles, readOverlayHandholds,
  readOverlayLabelsGlobal, readOverlayBadgesGlobal, readOverlayOverlaysVis,
  readOverlayDoubleLinks,
  // Event enum
  BUF_EVENT_RECV, BUF_EVENT_FIRE, BUF_EVENT_SEND, BUF_EVENT_ARRIVE, BUF_EVENT_DONE,
} from "../src/schema/buffer-layout";

// ─ helpers ──────────────────────────────────────────────────────────────────

/** Fused float32 approximately-equal check (matches what DataView.getFloat32 roundtrips). */
function expectF32(got: number, want: number): void {
  expect(got).toBeCloseTo(want, 5);
}

// ─ Bead block ─────────────────────────────────────────────────────────────────

describe("buffer-layout — Bead block", () => {
  it("stride equals packed field sizes", () => {
    // 3×f32 + i32 + f32 + u32 + u8 = 6×4 + 1 = 25
    expect(BEAD_STRIDE).toBe(25);
  });

  it("column offsets are contiguous packed", () => {
    expect(BEAD_COL_X).toBe(0);
    expect(BEAD_COL_Y).toBe(4);
    expect(BEAD_COL_Z).toBe(8);
    expect(BEAD_COL_VALUE).toBe(12);
    expect(BEAD_COL_FRAC).toBe(16);
    expect(BEAD_COL_BEAD_ID).toBe(20);
    expect(BEAD_COL_LIVE).toBe(24);
  });

  it("read helpers decode known bytes correctly (row 0 and row 1)", () => {
    const buf = new ArrayBuffer(BEAD_STRIDE * 2);
    const dv = new DataView(buf);

    // Row 0
    dv.setFloat32(0 * BEAD_STRIDE + BEAD_COL_X, 1.5, true);
    dv.setFloat32(0 * BEAD_STRIDE + BEAD_COL_Y, -2.25, true);
    dv.setFloat32(0 * BEAD_STRIDE + BEAD_COL_Z, 3.0, true);
    dv.setInt32(0 * BEAD_STRIDE + BEAD_COL_VALUE, -7, true);
    dv.setFloat32(0 * BEAD_STRIDE + BEAD_COL_FRAC, 0.75, true);
    dv.setUint32(0 * BEAD_STRIDE + BEAD_COL_BEAD_ID, 42, true);
    dv.setUint8(0 * BEAD_STRIDE + BEAD_COL_LIVE, 1);

    // Row 1 (different values to verify row indexing)
    dv.setFloat32(1 * BEAD_STRIDE + BEAD_COL_X, 10.0, true);
    dv.setFloat32(1 * BEAD_STRIDE + BEAD_COL_Y, 20.0, true);
    dv.setUint8(1 * BEAD_STRIDE + BEAD_COL_LIVE, 0);

    expectF32(readBeadX(dv, 0), 1.5);
    expectF32(readBeadY(dv, 0), -2.25);
    expectF32(readBeadZ(dv, 0), 3.0);
    expect(readBeadValue(dv, 0)).toBe(-7);
    expectF32(readBeadFrac(dv, 0), 0.75);
    expect(readBeadBeadID(dv, 0)).toBe(42);
    expect(readBeadLive(dv, 0)).toBe(1);

    expectF32(readBeadX(dv, 1), 10.0);
    expectF32(readBeadY(dv, 1), 20.0);
    expect(readBeadLive(dv, 1)).toBe(0);
  });
});

// ─ Node block ─────────────────────────────────────────────────────────────────

describe("buffer-layout — Node block", () => {
  it("stride equals packed field sizes", () => {
    // 5×f32 + u8 + i32 + 3×f32 + 5×u8 (events) + 1×u8 (selected)
    //   = (5+3)×4 + 1 + 4 + 5 + 1 = 43
    expect(NODE_STRIDE).toBe(43);
  });

  it("read helpers decode known bytes correctly", () => {
    const buf = new ArrayBuffer(NODE_STRIDE);
    const dv = new DataView(buf);

    dv.setFloat32(NODE_COL_CX, 1.0, true);
    dv.setFloat32(NODE_COL_CY, 2.0, true);
    dv.setFloat32(NODE_COL_CZ, 3.0, true);
    dv.setFloat32(NODE_COL_RADIUS, 0.5, true);
    dv.setFloat32(NODE_COL_SPHERE_R, 0.25, true);
    dv.setUint8(NODE_COL_TORUS_RED, 1);
    dv.setInt32(NODE_COL_MISS_VAL, -42, true);
    dv.setFloat32(NODE_COL_MX, 4.0, true);
    dv.setFloat32(NODE_COL_MY, 5.0, true);
    dv.setFloat32(NODE_COL_MZ, 6.0, true);
    dv.setUint8(NODE_COL_EV_RECV, 1);
    dv.setUint8(NODE_COL_EV_FIRE, 0);
    dv.setUint8(NODE_COL_EV_SEND, 1);
    dv.setUint8(NODE_COL_EV_ARRIVE, 0);
    dv.setUint8(NODE_COL_EV_DONE, 1);
    dv.setUint8(NODE_COL_SELECTED, 1);

    expectF32(readNodeCX(dv, 0), 1.0);
    expectF32(readNodeCY(dv, 0), 2.0);
    expectF32(readNodeCZ(dv, 0), 3.0);
    expectF32(readNodeRadius(dv, 0), 0.5);
    expectF32(readNodeSphereR(dv, 0), 0.25);
    expect(readNodeTorusRed(dv, 0)).toBe(1);
    expect(readNodeMissVal(dv, 0)).toBe(-42);
    expectF32(readNodeMX(dv, 0), 4.0);
    expectF32(readNodeMY(dv, 0), 5.0);
    expectF32(readNodeMZ(dv, 0), 6.0);
    expect(readNodeEvRecv(dv, 0)).toBe(1);
    expect(readNodeEvFire(dv, 0)).toBe(0);
    expect(readNodeEvSend(dv, 0)).toBe(1);
    expect(readNodeEvArrive(dv, 0)).toBe(0);
    expect(readNodeEvDone(dv, 0)).toBe(1);
    expect(readNodeSelected(dv, 0)).toBe(1);
  });
});

// ─ Edge block ─────────────────────────────────────────────────────────────────

describe("buffer-layout — Edge block", () => {
  it("stride equals packed field sizes", () => {
    // 6×f32 = 24
    expect(EDGE_STRIDE).toBe(24);
  });

  it("read helpers decode known bytes correctly", () => {
    const buf = new ArrayBuffer(EDGE_STRIDE);
    const dv = new DataView(buf);

    dv.setFloat32(EDGE_COL_SX, 1.0, true);
    dv.setFloat32(EDGE_COL_SY, 2.0, true);
    dv.setFloat32(EDGE_COL_SZ, 3.0, true);
    dv.setFloat32(EDGE_COL_EX, 4.0, true);
    dv.setFloat32(EDGE_COL_EY, 5.0, true);
    dv.setFloat32(EDGE_COL_EZ, 6.0, true);

    expectF32(readEdgeSX(dv, 0), 1.0);
    expectF32(readEdgeSY(dv, 0), 2.0);
    expectF32(readEdgeSZ(dv, 0), 3.0);
    expectF32(readEdgeEX(dv, 0), 4.0);
    expectF32(readEdgeEY(dv, 0), 5.0);
    expectF32(readEdgeEZ(dv, 0), 6.0);
  });
});

// ─ Camera block ───────────────────────────────────────────────────────────────

describe("buffer-layout — Camera block", () => {
  it("stride equals packed field sizes", () => {
    // 8×f32 = 32
    expect(CAMERA_STRIDE).toBe(32);
  });

  it("read helpers decode known bytes correctly", () => {
    const buf = new ArrayBuffer(CAMERA_STRIDE);
    const dv = new DataView(buf);

    dv.setFloat32(CAMERA_COL_PX, 1.0, true);
    dv.setFloat32(CAMERA_COL_PY, 2.0, true);
    dv.setFloat32(CAMERA_COL_PZ, 3.0, true);
    dv.setFloat32(CAMERA_COL_R, 10.0, true);
    dv.setFloat32(CAMERA_COL_POS_THETA, 0.5, true);
    dv.setFloat32(CAMERA_COL_POS_PHI, 1.0, true);
    dv.setFloat32(CAMERA_COL_UP_THETA, 0.25, true);
    dv.setFloat32(CAMERA_COL_UP_PHI, 0.75, true);

    expectF32(readCameraPX(dv), 1.0);
    expectF32(readCameraPY(dv), 2.0);
    expectF32(readCameraPZ(dv), 3.0);
    expectF32(readCameraR(dv), 10.0);
    expectF32(readCameraPosTheta(dv), 0.5);
    expectF32(readCameraPosPhi(dv), 1.0);
    expectF32(readCameraUpTheta(dv), 0.25);
    expectF32(readCameraUpPhi(dv), 0.75);
  });
});

// ─ Overlay block ──────────────────────────────────────────────────────────────

describe("buffer-layout — Overlay block", () => {
  it("stride equals packed field sizes", () => {
    // 10×u8 = 10
    expect(OVERLAY_STRIDE).toBe(10);
  });

  it("column offsets are 0..9", () => {
    expect(OVERLAY_COL_SCENE_TORI).toBe(0);
    expect(OVERLAY_COL_SCENE_POLES).toBe(1);
    expect(OVERLAY_COL_NODE_POLES).toBe(2);
    expect(OVERLAY_COL_ANGLE_LABELS).toBe(3);
    expect(OVERLAY_COL_SEL_SPHERE_POLES).toBe(4);
    expect(OVERLAY_COL_HANDHOLDS).toBe(5);
    expect(OVERLAY_COL_LABELS_GLOBAL).toBe(6);
    expect(OVERLAY_COL_BADGES_GLOBAL).toBe(7);
    expect(OVERLAY_COL_OVERLAYS_VIS).toBe(8);
    expect(OVERLAY_COL_DOUBLE_LINKS).toBe(9);
  });

  it("read helpers decode known bytes (alternating pattern)", () => {
    const buf = new ArrayBuffer(OVERLAY_STRIDE);
    const bytes = new Uint8Array(buf);
    // Alternating 1/0: sceneTori=1, scenePoles=0, nodePoles=1, ...
    ([1, 0, 1, 0, 1, 0, 1, 0, 1, 0] as const).forEach((v, i) => { bytes[i] = v; });

    const dv = new DataView(buf);
    expect(readOverlaySceneTori(dv)).toBe(1);
    expect(readOverlayScenePoles(dv)).toBe(0);
    expect(readOverlayNodePoles(dv)).toBe(1);
    expect(readOverlayAngleLabels(dv)).toBe(0);
    expect(readOverlaySelSpherePoles(dv)).toBe(1);
    expect(readOverlayHandholds(dv)).toBe(0);
    expect(readOverlayLabelsGlobal(dv)).toBe(1);
    expect(readOverlayBadgesGlobal(dv)).toBe(0);
    expect(readOverlayOverlaysVis(dv)).toBe(1);
    expect(readOverlayDoubleLinks(dv)).toBe(0);
  });
});

// ─ Event enum ─────────────────────────────────────────────────────────────────

describe("buffer-layout — event enum", () => {
  it("ids are 0-based contiguous", () => {
    expect(BUF_EVENT_RECV).toBe(0);
    expect(BUF_EVENT_FIRE).toBe(1);
    expect(BUF_EVENT_SEND).toBe(2);
    expect(BUF_EVENT_ARRIVE).toBe(3);
    expect(BUF_EVENT_DONE).toBe(4);
  });
});

// ─ Meta ───────────────────────────────────────────────────────────────────────

describe("buffer-layout — meta", () => {
  it("schema version is 1", () => {
    expect(BUF_LAYOUT_VERSION).toBe(2);
  });

  it("header size is 16 bytes (4×u32)", () => {
    expect(BUF_HEADER_SIZE).toBe(16);
  });
});
