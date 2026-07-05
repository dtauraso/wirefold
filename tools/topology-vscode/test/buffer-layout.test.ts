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
  NODE_COL_EV_RECV, NODE_COL_EV_FIRE, NODE_COL_EV_SEND, NODE_COL_EV_ARRIVE, NODE_COL_EV_DONE,
  NODE_COL_SELECTED,
  NODE_COL_KIND_ID,
  NODE_COL_LABEL_OFF, NODE_COL_LABEL_LEN, NODE_COL_FADED, NODE_COL_HOVERED,
  NODE_STRIDE,
  NODE_COL_VRX, NODE_COL_VRY, NODE_COL_VRZ, NODE_COL_FRX, NODE_COL_FRY, NODE_COL_FRZ,
  readNodeCX, readNodeCY, readNodeCZ, readNodeRadius, readNodeSphereR,
  readNodeVRX, readNodeVRY, readNodeVRZ, readNodeFRX, readNodeFRY, readNodeFRZ,
  readNodeEvRecv, readNodeEvFire, readNodeEvSend, readNodeEvArrive, readNodeEvDone,
  readNodeSelected,
  readNodeKindId,
  readNodeLabelOff, readNodeLabelLen, readNodeFaded, readNodeHovered,
  // Interior
  INTERIOR_COL_PRESENT, INTERIOR_COL_VALUE, INTERIOR_COL_OX, INTERIOR_COL_OY, INTERIOR_COL_OZ,
  INTERIOR_STRIDE,
  readInteriorPresent, readInteriorValue, readInteriorOX, readInteriorOY, readInteriorOZ,
  // Edge
  EDGE_COL_SX, EDGE_COL_SY, EDGE_COL_SZ, EDGE_COL_EX, EDGE_COL_EY, EDGE_COL_EZ,
  EDGE_COL_SRC_NODE_ROW, EDGE_COL_DST_NODE_ROW, EDGE_COL_SELECTED, EDGE_COL_FADED,
  EDGE_STRIDE,
  readEdgeSX, readEdgeSY, readEdgeSZ, readEdgeEX, readEdgeEY, readEdgeEZ,
  readEdgeSrcNodeRow, readEdgeDstNodeRow, readEdgeSelected, readEdgeFaded,
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
  OVERLAY_COL_DOUBLE_LINKS, OVERLAY_COL_SEL_MODE, OVERLAY_STRIDE,
  readOverlaySceneTori, readOverlayScenePoles, readOverlayNodePoles,
  readOverlayAngleLabels, readOverlaySelSpherePoles, readOverlayHandholds,
  readOverlayLabelsGlobal, readOverlayBadgesGlobal, readOverlayOverlaysVis,
  readOverlayDoubleLinks, readOverlaySelMode,
  // RuleBuilder block
  RULE_BUILDER_COL_CENTER_ROW, RULE_BUILDER_COL_PENDING_CODE, RULE_BUILDER_COL_TERM_COUNT,
  RULE_BUILDER_COL_T0ROW, RULE_BUILDER_COL_T0CODE, RULE_BUILDER_COL_T1ROW, RULE_BUILDER_COL_T1CODE,
  RULE_BUILDER_COL_SELECTED_LOCK_INDEX,
  RULE_BUILDER_COL_PENDING_PORT_ROW, RULE_BUILDER_COL_PENDING_PORT_IS_INPUT, RULE_BUILDER_COL_PENDING_TORUS_ROW,
  RULE_BUILDER_STRIDE,
  readRuleBuilderCenterRow, readRuleBuilderPendingCode, readRuleBuilderTermCount,
  readRuleBuilderT0Row, readRuleBuilderT0Code, readRuleBuilderT1Row, readRuleBuilderT1Code,
  readRuleBuilderSelectedLockIndex,
  readRuleBuilderPendingPortRow, readRuleBuilderPendingPortIsInput, readRuleBuilderPendingTorusRow,
  // PolarLock block
  POLAR_LOCK_COL_CENTER_ROW, POLAR_LOCK_COL_AROW, POLAR_LOCK_COL_ACODE,
  POLAR_LOCK_COL_BROW, POLAR_LOCK_COL_BCODE, POLAR_LOCK_COL_ACTIVE, POLAR_LOCK_STRIDE,
  POLAR_LOCK_COL_KIND, POLAR_LOCK_COL_PORT_ROW, POLAR_LOCK_COL_PORT_IS_INPUT, POLAR_LOCK_COL_TORUS_ROW,
  POLAR_LOCK_COL_SELECTED, POLAR_LOCK_COL_OWNED,
  readPolarLockCenterRow, readPolarLockARow, readPolarLockACode,
  readPolarLockBRow, readPolarLockBCode, readPolarLockActive,
  readPolarLockKind, readPolarLockPortRow, readPolarLockPortIsInput, readPolarLockTorusRow,
  readPolarLockSelected,
  readPolarLockOwned,
  // Port block
  PORT_COL_NODE_ROW, PORT_COL_IS_INPUT, PORT_COL_HOVERED, PORT_STRIDE,
  readPortNodeRow, readPortIsInput, readPortHovered,
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
    // 5×f32 + 6×f32 (vr/fr normals) + 5×u8 (events) + 1×u8 (selected)
    //   + 1×u8 (kindId) + 2×u32 (label off/len)
    //   + 1×u8 (faded) + 1×u8 (hovered)
    //   = (5+6)×4 + 5 + 1 + 1 + 8 + 1 + 1 = 61
    expect(NODE_STRIDE).toBe(61);
  });

  it("read helpers decode known bytes correctly", () => {
    const buf = new ArrayBuffer(NODE_STRIDE);
    const dv = new DataView(buf);

    dv.setFloat32(NODE_COL_CX, 1.0, true);
    dv.setFloat32(NODE_COL_CY, 2.0, true);
    dv.setFloat32(NODE_COL_CZ, 3.0, true);
    dv.setFloat32(NODE_COL_RADIUS, 0.5, true);
    dv.setFloat32(NODE_COL_SPHERE_R, 0.25, true);
    dv.setFloat32(NODE_COL_VRX, 0.1, true);
    dv.setFloat32(NODE_COL_VRY, 0.2, true);
    dv.setFloat32(NODE_COL_VRZ, 0.3, true);
    dv.setFloat32(NODE_COL_FRX, 0.4, true);
    dv.setFloat32(NODE_COL_FRY, 0.5, true);
    dv.setFloat32(NODE_COL_FRZ, 0.6, true);
    dv.setUint8(NODE_COL_EV_RECV, 1);
    dv.setUint8(NODE_COL_EV_FIRE, 0);
    dv.setUint8(NODE_COL_EV_SEND, 1);
    dv.setUint8(NODE_COL_EV_ARRIVE, 0);
    dv.setUint8(NODE_COL_EV_DONE, 1);
    dv.setUint8(NODE_COL_SELECTED, 1);
    dv.setUint8(NODE_COL_KIND_ID, 5); // Pulse = index 5
    dv.setUint32(NODE_COL_LABEL_OFF, 7, true);
    dv.setUint32(NODE_COL_LABEL_LEN, 4, true);
    dv.setUint8(NODE_COL_FADED, 1);
    dv.setUint8(NODE_COL_HOVERED, 1);

    expectF32(readNodeCX(dv, 0), 1.0);
    expectF32(readNodeCY(dv, 0), 2.0);
    expectF32(readNodeCZ(dv, 0), 3.0);
    expectF32(readNodeRadius(dv, 0), 0.5);
    expectF32(readNodeSphereR(dv, 0), 0.25);
    expectF32(readNodeVRX(dv, 0), 0.1);
    expectF32(readNodeVRY(dv, 0), 0.2);
    expectF32(readNodeVRZ(dv, 0), 0.3);
    expectF32(readNodeFRX(dv, 0), 0.4);
    expectF32(readNodeFRY(dv, 0), 0.5);
    expectF32(readNodeFRZ(dv, 0), 0.6);
    expect(readNodeEvRecv(dv, 0)).toBe(1);
    expect(readNodeEvFire(dv, 0)).toBe(0);
    expect(readNodeEvSend(dv, 0)).toBe(1);
    expect(readNodeEvArrive(dv, 0)).toBe(0);
    expect(readNodeEvDone(dv, 0)).toBe(1);
    expect(readNodeSelected(dv, 0)).toBe(1);
    expect(readNodeKindId(dv, 0)).toBe(5);
    expect(readNodeLabelOff(dv, 0)).toBe(7);
    expect(readNodeLabelLen(dv, 0)).toBe(4);
    expect(readNodeFaded(dv, 0)).toBe(1);
    expect(readNodeHovered(dv, 0)).toBe(1);
  });
});

// ─ Port block ─────────────────────────────────────────────────────────────────

describe("buffer-layout — Port block", () => {
  it("stride equals packed field sizes", () => {
    // i32 (nodeRow) + 3×f32 (DX/DY/DZ) + 3×f32 (PX/PY/PZ) + u8 (isInput) + u8 (hovered)
    // + 2×u32 (port-name off/len) = 4 + 12 + 12 + 1 + 1 + 8 = 38
    expect(PORT_STRIDE).toBe(38);
  });

  it("read helpers decode isInput + hovered", () => {
    const buf = new ArrayBuffer(PORT_STRIDE);
    const dv = new DataView(buf);
    dv.setInt32(PORT_COL_NODE_ROW, 2, true);
    dv.setUint8(PORT_COL_IS_INPUT, 1);
    dv.setUint8(PORT_COL_HOVERED, 1);
    expect(readPortNodeRow(dv, 0)).toBe(2);
    expect(readPortIsInput(dv, 0)).toBe(1);
    expect(readPortHovered(dv, 0)).toBe(1);
  });
});

// ─ Interior block ─────────────────────────────────────────────────────────────

describe("buffer-layout — Interior block", () => {
  it("stride equals packed field sizes", () => {
    // u8 + i32 + 3×f32 = 1 + 4 + 12 = 17
    expect(INTERIOR_STRIDE).toBe(17);
  });

  it("read helpers decode known bytes correctly", () => {
    const buf = new ArrayBuffer(INTERIOR_STRIDE);
    const dv = new DataView(buf);

    dv.setUint8(INTERIOR_COL_PRESENT, 1);
    dv.setInt32(INTERIOR_COL_VALUE, 1, true);
    dv.setFloat32(INTERIOR_COL_OX, 2.5, true);
    dv.setFloat32(INTERIOR_COL_OY, -3.5, true);
    dv.setFloat32(INTERIOR_COL_OZ, 4.5, true);

    expect(readInteriorPresent(dv, 0)).toBe(1);
    expect(readInteriorValue(dv, 0)).toBe(1);
    expectF32(readInteriorOX(dv, 0), 2.5);
    expectF32(readInteriorOY(dv, 0), -3.5);
    expectF32(readInteriorOZ(dv, 0), 4.5);
  });
});

// ─ Edge block ─────────────────────────────────────────────────────────────────

describe("buffer-layout — Edge block", () => {
  it("stride equals packed field sizes", () => {
    // 6×f32 + 2×i32 + 2×u8 (selected + faded) + 2×u32 (edge-label off/len) = 42
    expect(EDGE_STRIDE).toBe(42);
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
    dv.setInt32(EDGE_COL_SRC_NODE_ROW, 2, true);
    dv.setInt32(EDGE_COL_DST_NODE_ROW, -1, true);
    dv.setUint8(EDGE_COL_SELECTED, 1);
    dv.setUint8(EDGE_COL_FADED, 1);

    expectF32(readEdgeSX(dv, 0), 1.0);
    expectF32(readEdgeSY(dv, 0), 2.0);
    expectF32(readEdgeSZ(dv, 0), 3.0);
    expectF32(readEdgeEX(dv, 0), 4.0);
    expectF32(readEdgeEY(dv, 0), 5.0);
    expectF32(readEdgeEZ(dv, 0), 6.0);
    expect(readEdgeSrcNodeRow(dv, 0)).toBe(2);
    expect(readEdgeDstNodeRow(dv, 0)).toBe(-1);
    expect(readEdgeSelected(dv, 0)).toBe(1);
    expect(readEdgeFaded(dv, 0)).toBe(1);
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
    // 11×u8 = 11 (10 overlay flags + SelMode)
    expect(OVERLAY_STRIDE).toBe(11);
  });

  it("column offsets are 0..10", () => {
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
    expect(OVERLAY_COL_SEL_MODE).toBe(10);
  });

  it("read helpers decode known bytes (alternating pattern)", () => {
    const buf = new ArrayBuffer(OVERLAY_STRIDE);
    const bytes = new Uint8Array(buf);
    // Alternating 1/0: sceneTori=1, scenePoles=0, nodePoles=1, ..., selMode=1.
    ([1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1] as const).forEach((v, i) => { bytes[i] = v; });

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
    expect(readOverlaySelMode(dv)).toBe(1);
  });
});

// ─ RuleBuilder block ────────────────────────────────────────────────────────────

describe("buffer-layout — RuleBuilder block", () => {
  it("stride equals packed field sizes", () => {
    // i32 + u8 + u8 + i32 + u8 + i32 + u8 + i32 (SelectedLockIndex) + i32 (PendingPortRow)
    // + u8 (PendingPortIsInput) + i32 (PendingTorusRow) = 4+1+1+4+1+4+1+4+4+1+4 = 29
    expect(RULE_BUILDER_STRIDE).toBe(29);
  });

  it("column offsets match the packed i32/u8 layout", () => {
    expect(RULE_BUILDER_COL_CENTER_ROW).toBe(0);
    expect(RULE_BUILDER_COL_PENDING_CODE).toBe(4);
    expect(RULE_BUILDER_COL_TERM_COUNT).toBe(5);
    expect(RULE_BUILDER_COL_T0ROW).toBe(6);
    expect(RULE_BUILDER_COL_T0CODE).toBe(10);
    expect(RULE_BUILDER_COL_T1ROW).toBe(11);
    expect(RULE_BUILDER_COL_T1CODE).toBe(15);
    expect(RULE_BUILDER_COL_SELECTED_LOCK_INDEX).toBe(16);
    expect(RULE_BUILDER_COL_PENDING_PORT_ROW).toBe(20);
    expect(RULE_BUILDER_COL_PENDING_PORT_IS_INPUT).toBe(24);
    expect(RULE_BUILDER_COL_PENDING_TORUS_ROW).toBe(25);
  });

  it("read helpers decode a completed term + pending half-term", () => {
    const buf = new ArrayBuffer(RULE_BUILDER_STRIDE);
    const dv = new DataView(buf);
    dv.setInt32(RULE_BUILDER_COL_CENTER_ROW, 3, true); // Center = node row 3
    dv.setUint8(RULE_BUILDER_COL_PENDING_CODE, 2);     // pending: −θ
    dv.setUint8(RULE_BUILDER_COL_TERM_COUNT, 1);
    dv.setInt32(RULE_BUILDER_COL_T0ROW, 5, true);      // term 0 = node row 5
    dv.setUint8(RULE_BUILDER_COL_T0CODE, 1);           // +φ
    dv.setInt32(RULE_BUILDER_COL_T1ROW, -1, true);     // absent
    dv.setUint8(RULE_BUILDER_COL_T1CODE, 255);         // absent

    expect(readRuleBuilderCenterRow(dv)).toBe(3);
    expect(readRuleBuilderPendingCode(dv)).toBe(2);
    expect(readRuleBuilderTermCount(dv)).toBe(1);
    expect(readRuleBuilderT0Row(dv)).toBe(5);
    expect(readRuleBuilderT0Code(dv)).toBe(1);
    expect(readRuleBuilderT1Row(dv)).toBe(-1);
    expect(readRuleBuilderT1Code(dv)).toBe(255);
  });

  it("reads the SelectedLockIndex column", () => {
    const buf = new ArrayBuffer(RULE_BUILDER_STRIDE);
    const dv = new DataView(buf);
    dv.setInt32(RULE_BUILDER_COL_SELECTED_LOCK_INDEX, 2, true);
    expect(readRuleBuilderSelectedLockIndex(dv)).toBe(2);
  });

  it("reads the pending port/torus authoring columns", () => {
    const buf = new ArrayBuffer(RULE_BUILDER_STRIDE);
    const dv = new DataView(buf);
    dv.setInt32(RULE_BUILDER_COL_PENDING_PORT_ROW, 7, true);
    dv.setUint8(RULE_BUILDER_COL_PENDING_PORT_IS_INPUT, 1);
    dv.setInt32(RULE_BUILDER_COL_PENDING_TORUS_ROW, -1, true);

    expect(readRuleBuilderPendingPortRow(dv)).toBe(7);
    expect(readRuleBuilderPendingPortIsInput(dv)).toBe(1);
    expect(readRuleBuilderPendingTorusRow(dv)).toBe(-1);
  });
});

// ─ PolarLock block ──────────────────────────────────────────────────────────────

describe("buffer-layout — PolarLock block", () => {
  it("stride equals packed field sizes", () => {
    // 5×i32 (CenterRow/ARow/BRow/PortRow/TorusRow) +
    // 7×u8 (ACode/BCode/Active/Kind/PortIsInput/Selected/Owned) = 5*4 + 7 = 27
    expect(POLAR_LOCK_STRIDE).toBe(27);
  });

  it("column offsets match the packed i32/u8 layout", () => {
    expect(POLAR_LOCK_COL_CENTER_ROW).toBe(0);
    expect(POLAR_LOCK_COL_AROW).toBe(4);
    expect(POLAR_LOCK_COL_ACODE).toBe(8);
    expect(POLAR_LOCK_COL_BROW).toBe(9);
    expect(POLAR_LOCK_COL_BCODE).toBe(13);
    expect(POLAR_LOCK_COL_ACTIVE).toBe(14);
    expect(POLAR_LOCK_COL_KIND).toBe(15);
    expect(POLAR_LOCK_COL_PORT_ROW).toBe(16);
    expect(POLAR_LOCK_COL_PORT_IS_INPUT).toBe(20);
    expect(POLAR_LOCK_COL_TORUS_ROW).toBe(21);
    expect(POLAR_LOCK_COL_SELECTED).toBe(25);
    expect(POLAR_LOCK_COL_OWNED).toBe(26);
  });

  it("read helpers decode a committed node/node equation row", () => {
    const buf = new ArrayBuffer(POLAR_LOCK_STRIDE);
    const dv = new DataView(buf);
    dv.setInt32(POLAR_LOCK_COL_CENTER_ROW, 3, true);
    dv.setInt32(POLAR_LOCK_COL_AROW, 1, true);
    dv.setUint8(POLAR_LOCK_COL_ACODE, 0);
    dv.setInt32(POLAR_LOCK_COL_BROW, 2, true);
    dv.setUint8(POLAR_LOCK_COL_BCODE, 2);
    dv.setUint8(POLAR_LOCK_COL_ACTIVE, 1);
    dv.setUint8(POLAR_LOCK_COL_KIND, 0);
    dv.setInt32(POLAR_LOCK_COL_PORT_ROW, -1, true);
    dv.setInt32(POLAR_LOCK_COL_TORUS_ROW, -1, true);
    dv.setUint8(POLAR_LOCK_COL_SELECTED, 1);
    dv.setUint8(POLAR_LOCK_COL_OWNED, 1);

    expect(readPolarLockCenterRow(dv, 0)).toBe(3);
    expect(readPolarLockARow(dv, 0)).toBe(1);
    expect(readPolarLockACode(dv, 0)).toBe(0);
    expect(readPolarLockBRow(dv, 0)).toBe(2);
    expect(readPolarLockBCode(dv, 0)).toBe(2);
    expect(readPolarLockActive(dv, 0)).toBe(1);
    expect(readPolarLockKind(dv, 0)).toBe(0);
    expect(readPolarLockSelected(dv, 0)).toBe(1);
    expect(readPolarLockOwned(dv, 0)).toBe(1);
  });

  it("read helpers decode a committed port∈torus lock row", () => {
    const buf = new ArrayBuffer(POLAR_LOCK_STRIDE);
    const dv = new DataView(buf);
    dv.setInt32(POLAR_LOCK_COL_CENTER_ROW, -1, true);
    dv.setInt32(POLAR_LOCK_COL_AROW, -1, true);
    dv.setInt32(POLAR_LOCK_COL_BROW, -1, true);
    dv.setUint8(POLAR_LOCK_COL_ACTIVE, 1);
    dv.setUint8(POLAR_LOCK_COL_KIND, 1);
    dv.setInt32(POLAR_LOCK_COL_PORT_ROW, 5, true);
    dv.setUint8(POLAR_LOCK_COL_PORT_IS_INPUT, 1);
    dv.setInt32(POLAR_LOCK_COL_TORUS_ROW, 3, true);
    dv.setUint8(POLAR_LOCK_COL_SELECTED, 0);
    dv.setUint8(POLAR_LOCK_COL_OWNED, 0);

    expect(readPolarLockKind(dv, 0)).toBe(1);
    expect(readPolarLockPortRow(dv, 0)).toBe(5);
    expect(readPolarLockPortIsInput(dv, 0)).toBe(1);
    expect(readPolarLockTorusRow(dv, 0)).toBe(3);
    expect(readPolarLockSelected(dv, 0)).toBe(0);
    expect(readPolarLockOwned(dv, 0)).toBe(0);
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
  it("schema version is 19", () => {
    expect(BUF_LAYOUT_VERSION).toBe(19);
  });

  it("header size is 40 bytes (10×u32)", () => {
    expect(BUF_HEADER_SIZE).toBe(40);
  });
});
