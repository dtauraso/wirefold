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
  BEAD_COL_X, BEAD_COL_Y, BEAD_COL_Z, BEAD_COL_VALUE,
  BEAD_COL_LIVE, BEAD_STRIDE,
  readBeadX, readBeadY, readBeadZ, readBeadValue,
  readBeadLive,
  // Node
  NODE_COL_CX, NODE_COL_CY, NODE_COL_CZ, NODE_COL_RADIUS, NODE_COL_SPHERE_R,
  NODE_COL_SELECTED,
  NODE_COL_KIND_ID,
  NODE_COL_LABEL_OFF, NODE_COL_LABEL_LEN, NODE_COL_HOVERED,
  NODE_COL_GOT_DRAG_MSG,
  NODE_COL_DRAG_DELTA_A, NODE_COL_DRAG_DELTA_B, NODE_COL_DRAG_DELTA_C,
  NODE_STRIDE,
  NODE_COL_VRX, NODE_COL_VRY, NODE_COL_VRZ, NODE_COL_FRX, NODE_COL_FRY, NODE_COL_FRZ,
  readNodeCX, readNodeCY, readNodeCZ, readNodeRadius, readNodeSphereR,
  readNodeVRX, readNodeVRY, readNodeVRZ, readNodeFRX, readNodeFRY, readNodeFRZ,
  readNodeSelected,
  readNodeKindId,
  readNodeLabelOff, readNodeLabelLen, readNodeHovered,
  readNodeGotDragMsg,
  readNodeDragDeltaA, readNodeDragDeltaB, readNodeDragDeltaC,
  // Interior
  INTERIOR_COL_PRESENT, INTERIOR_COL_VALUE, INTERIOR_COL_OX, INTERIOR_COL_OY, INTERIOR_COL_OZ,
  INTERIOR_STRIDE,
  readInteriorPresent, readInteriorValue, readInteriorOX, readInteriorOY, readInteriorOZ,
  // Edge
  EDGE_COL_SRC_PORT_ROW, EDGE_COL_DST_PORT_ROW,
  EDGE_COL_SELECTED,
  EDGE_STRIDE,
  readEdgeSrcPortRow, readEdgeDstPortRow,
  readEdgeSelected,
  // Camera
  CAMERA_COL_PX, CAMERA_COL_PY, CAMERA_COL_PZ, CAMERA_COL_R,
  CAMERA_COL_POS_THETA, CAMERA_COL_POS_PHI, CAMERA_COL_UP_THETA, CAMERA_COL_UP_PHI,
  CAMERA_STRIDE,
  readCameraPX, readCameraPY, readCameraPZ, readCameraR,
  readCameraPosTheta, readCameraPosPhi, readCameraUpTheta, readCameraUpPhi,
  // Overlay
  OVERLAY_COL_SCENE_TORI, OVERLAY_COL_SCENE_POLES, OVERLAY_COL_NODE_POLES,
  OVERLAY_COL_SEL_SPHERE_POLES, OVERLAY_COL_HANDHOLDS,
  OVERLAY_COL_LABELS_GLOBAL, OVERLAY_COL_OVERLAYS_VIS,
  OVERLAY_COL_DOUBLE_LINKS,
  OVERLAY_COL_ABC_DRAG_COUNT,
  OVERLAY_STRIDE,
  readOverlaySceneTori, readOverlayScenePoles, readOverlayNodePoles,
  readOverlaySelSpherePoles, readOverlayHandholds,
  readOverlayLabelsGlobal, readOverlayOverlaysVis,
  readOverlayDoubleLinks,
  readOverlayAbcDragCount,
  // Port block
  PORT_COL_NODE_ROW, PORT_COL_IS_INPUT, PORT_COL_HOVERED, PORT_STRIDE,
  readPortNodeRow, readPortIsInput, readPortHovered,
} from "../src/schema/buffer-layout";

// ─ helpers ──────────────────────────────────────────────────────────────────

/** Fused float32 approximately-equal check (matches what DataView.getFloat32 roundtrips). */
function expectF32(got: number, want: number): void {
  expect(got).toBeCloseTo(want, 5);
}

// ─ Bead block ─────────────────────────────────────────────────────────────────

describe("buffer-layout — Bead block", () => {
  it("stride equals packed field sizes", () => {
    // 3×f32 + i32 + u8 = 4×4 + 1 = 17
    expect(BEAD_STRIDE).toBe(17);
  });

  it("column offsets are contiguous packed", () => {
    expect(BEAD_COL_X).toBe(0);
    expect(BEAD_COL_Y).toBe(4);
    expect(BEAD_COL_Z).toBe(8);
    expect(BEAD_COL_VALUE).toBe(12);
    expect(BEAD_COL_LIVE).toBe(16);
  });

  it("read helpers decode known bytes correctly (row 0 and row 1)", () => {
    const buf = new ArrayBuffer(BEAD_STRIDE * 2);
    const dv = new DataView(buf);

    // Row 0
    dv.setFloat32(0 * BEAD_STRIDE + BEAD_COL_X, 1.5, true);
    dv.setFloat32(0 * BEAD_STRIDE + BEAD_COL_Y, -2.25, true);
    dv.setFloat32(0 * BEAD_STRIDE + BEAD_COL_Z, 3.0, true);
    dv.setInt32(0 * BEAD_STRIDE + BEAD_COL_VALUE, -7, true);
    dv.setUint8(0 * BEAD_STRIDE + BEAD_COL_LIVE, 1);

    // Row 1 (different values to verify row indexing)
    dv.setFloat32(1 * BEAD_STRIDE + BEAD_COL_X, 10.0, true);
    dv.setFloat32(1 * BEAD_STRIDE + BEAD_COL_Y, 20.0, true);
    dv.setUint8(1 * BEAD_STRIDE + BEAD_COL_LIVE, 0);

    expectF32(readBeadX(dv, 0), 1.5);
    expectF32(readBeadY(dv, 0), -2.25);
    expectF32(readBeadZ(dv, 0), 3.0);
    expect(readBeadValue(dv, 0)).toBe(-7);
    expect(readBeadLive(dv, 0)).toBe(1);

    expectF32(readBeadX(dv, 1), 10.0);
    expectF32(readBeadY(dv, 1), 20.0);
    expect(readBeadLive(dv, 1)).toBe(0);
  });
});

// ─ Node block ─────────────────────────────────────────────────────────────────

describe("buffer-layout — Node block", () => {
  it("stride equals packed field sizes", () => {
    // 5×f32 + 6×f32 (vr/fr normals) + 1×u8 (selected)
    //   + 1×u8 (kindId) + 2×u32 (label off/len)
    //   + 1×u8 (hovered) + 1×u8 (latchedSel) + 1×u8 (gotDragMsg)
    //   + 3×i32 (dragDeltaA/B/C)
    //   = (5+6)×4 + 1 + 1 + 8 + 1 + 1 + 1 + 12 = 69
    expect(NODE_STRIDE).toBe(69);
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
    dv.setUint8(NODE_COL_SELECTED, 1);
    dv.setUint8(NODE_COL_KIND_ID, 5); // Pulse = index 5
    dv.setUint32(NODE_COL_LABEL_OFF, 7, true);
    dv.setUint32(NODE_COL_LABEL_LEN, 4, true);
    dv.setUint8(NODE_COL_HOVERED, 1);
    dv.setUint8(NODE_COL_GOT_DRAG_MSG, 1);
    dv.setInt32(NODE_COL_DRAG_DELTA_A, 12, true);
    dv.setInt32(NODE_COL_DRAG_DELTA_B, -34, true);
    dv.setInt32(NODE_COL_DRAG_DELTA_C, 5, true);

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
    expect(readNodeSelected(dv, 0)).toBe(1);
    expect(readNodeKindId(dv, 0)).toBe(5);
    expect(readNodeLabelOff(dv, 0)).toBe(7);
    expect(readNodeLabelLen(dv, 0)).toBe(4);
    expect(readNodeHovered(dv, 0)).toBe(1);
    expect(readNodeGotDragMsg(dv, 0)).toBe(1);
    expect(readNodeDragDeltaA(dv, 0)).toBe(12);
    expect(readNodeDragDeltaB(dv, 0)).toBe(-34);
    expect(readNodeDragDeltaC(dv, 0)).toBe(5);
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
    // 2×i32 (SrcPortRow/DstPortRow) + 1×u8 (selected) + 2×u32 (edge-label off/len) = 17.
    // No endpoint coordinates — the edge references its two port rows instead of storing
    // a copy (see bufLayoutEdge's doc comment, Buffer/layout.go — the endpoint-tear fix).
    expect(EDGE_STRIDE).toBe(17);
  });

  it("read helpers decode known bytes correctly", () => {
    const buf = new ArrayBuffer(EDGE_STRIDE);
    const dv = new DataView(buf);

    dv.setInt32(EDGE_COL_SRC_PORT_ROW, 3, true);
    dv.setInt32(EDGE_COL_DST_PORT_ROW, 7, true);
    dv.setUint8(EDGE_COL_SELECTED, 1);

    expect(readEdgeSrcPortRow(dv, 0)).toBe(3);
    expect(readEdgeDstPortRow(dv, 0)).toBe(7);
    expect(readEdgeSelected(dv, 0)).toBe(1);
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
    // 8×u8 + 1×u32 = 12 (8 overlay flags + AbcDragCount)
    expect(OVERLAY_STRIDE).toBe(12);
  });

  it("column offsets are 0..7", () => {
    expect(OVERLAY_COL_SCENE_TORI).toBe(0);
    expect(OVERLAY_COL_SCENE_POLES).toBe(1);
    expect(OVERLAY_COL_NODE_POLES).toBe(2);
    expect(OVERLAY_COL_SEL_SPHERE_POLES).toBe(3);
    expect(OVERLAY_COL_HANDHOLDS).toBe(4);
    expect(OVERLAY_COL_LABELS_GLOBAL).toBe(5);
    expect(OVERLAY_COL_OVERLAYS_VIS).toBe(6);
    expect(OVERLAY_COL_DOUBLE_LINKS).toBe(7);
    expect(OVERLAY_COL_ABC_DRAG_COUNT).toBe(8);
  });

  it("read helpers decode known bytes (alternating pattern)", () => {
    const buf = new ArrayBuffer(OVERLAY_STRIDE);
    const bytes = new Uint8Array(buf);
    // Alternating 1/0: sceneTori=1, scenePoles=0, nodePoles=1, ..., doubleLinks=0.
    ([1, 0, 1, 0, 1, 0, 1, 0] as const).forEach((v, i) => { bytes[i] = v; });

    const dv = new DataView(buf);
    dv.setUint32(OVERLAY_COL_ABC_DRAG_COUNT, 7, true);
    expect(readOverlaySceneTori(dv)).toBe(1);
    expect(readOverlayScenePoles(dv)).toBe(0);
    expect(readOverlayNodePoles(dv)).toBe(1);
    expect(readOverlaySelSpherePoles(dv)).toBe(0);
    expect(readOverlayHandholds(dv)).toBe(1);
    expect(readOverlayLabelsGlobal(dv)).toBe(0);
    expect(readOverlayOverlaysVis(dv)).toBe(1);
    expect(readOverlayDoubleLinks(dv)).toBe(0);
    expect(readOverlayAbcDragCount(dv)).toBe(7);
  });
});

// ─ Meta ───────────────────────────────────────────────────────────────────────

describe("buffer-layout — meta", () => {
  it("schema version is 33", () => {
    expect(BUF_LAYOUT_VERSION).toBe(33);
  });

  it("header size is 8 bytes (2×u32: tick + layoutLinkCount; no beadCount/nodeCount/portCount/labelBytesCount/portNameBytesCount/edgeCount/edgeLabelBytesCount/eventCount — beads, the node-owner-group blocks, the Edge block, and events are their own tagged/per-owner frames)", () => {
    expect(BUF_HEADER_SIZE).toBe(8);
  });
});
