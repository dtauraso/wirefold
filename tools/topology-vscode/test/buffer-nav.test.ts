// Unit tests for buffer-nav.ts — the pure decodeNavNodes + scene sphere.
//
// Builds raw NODE-frame ArrayBuffers matching the Go node-block layout and raw SCENE-frame
// ArrayBuffers matching the Go Scene block layout, and asserts:
//   - decodeNavNodes maps buffer row i to NavNode i (identity is the row index)
//   - the per-node label decodes from the buffer's trailing label section
//   - sphereR==0 decodes to undefined (old-path "missing" semantics)
//   - sceneSphereFromSnapshot reads the Go-owned Scene block (center/radius, SCENE frame),
//     NOT a TS-derived centroid over node centers

import { describe, it, expect } from "vitest";
import { decodeViewFrame, decodeNodeFrame } from "../src/webview/three/buffer-decode";
import {
  decodeNavNodes, sceneSphereFromSnapshot,
} from "../src/webview/three/buffer-nav";
import {
  NODE_STRIDE, INTERIOR_STRIDE, CAMERA_STRIDE, OVERLAY_STRIDE, SCENE_STRIDE,
  NODE_COL_CX, NODE_COL_CY, NODE_COL_CZ, NODE_COL_RADIUS,
  NODE_COL_SPHERE_R, NODE_COL_SELECTED, NODE_COL_LABEL_OFF, NODE_COL_LABEL_LEN,
  SCENE_COL_CX, SCENE_COL_CY, SCENE_COL_CZ, SCENE_COL_RADIUS,
} from "../src/schema/buffer-layout";
import { BUF_NODE_FRAME_HEADER_SIZE, BUF_VIEW_FRAME_HEADER_SIZE } from "../src/schema/frame-tags";
import { INTERIOR_SLOTS_PER_NODE } from "../src/webview/three/buffer-decode";

type NodeFields = {
  cx?: number; cy?: number; cz?: number; radius?: number; sphereR?: number;
  selected?: number; label?: string;
};

type SceneFields = { cx?: number; cy?: number; cz?: number; radius?: number };

// Build a NODE frame (BUF_BLOCK_TAG_NODE) with `nodeCount` node rows. Labels are
// concatenated into the trailing label section and each node's LabelOff/LabelLen columns
// point into it.
function makeNodeFrame(nodeCount: number, fields: NodeFields[]): ArrayBuffer {
  const nodeBytes = nodeCount * NODE_STRIDE;
  const interiorBytes = nodeCount * INTERIOR_SLOTS_PER_NODE * INTERIOR_STRIDE;
  const enc = new TextEncoder();
  const labelChunks = fields.map((f) => enc.encode(f.label ?? ""));
  const labelBytesCount = labelChunks.reduce((n, c) => n + c.length, 0);
  const total = BUF_NODE_FRAME_HEADER_SIZE + nodeBytes + interiorBytes + labelBytesCount;
  const buf = new ArrayBuffer(total);
  const dv = new DataView(buf);
  dv.setUint32(4, nodeCount, true);        // nodeCount header field
  dv.setUint32(12, labelBytesCount, true); // labelBytesCount header field
  const nodeOff = BUF_NODE_FRAME_HEADER_SIZE;
  const labelSecOff = nodeOff + nodeBytes + interiorBytes;
  const labelView = new Uint8Array(buf, labelSecOff, labelBytesCount);
  let labelCursor = 0;
  fields.forEach((f, row) => {
    const base = nodeOff + row * NODE_STRIDE;
    if (f.cx !== undefined) dv.setFloat32(base + NODE_COL_CX, f.cx, true);
    if (f.cy !== undefined) dv.setFloat32(base + NODE_COL_CY, f.cy, true);
    if (f.cz !== undefined) dv.setFloat32(base + NODE_COL_CZ, f.cz, true);
    if (f.radius !== undefined) dv.setFloat32(base + NODE_COL_RADIUS, f.radius, true);
    if (f.sphereR !== undefined) dv.setFloat32(base + NODE_COL_SPHERE_R, f.sphereR, true);
    if (f.selected !== undefined) dv.setUint8(base + NODE_COL_SELECTED, f.selected);
    const chunk = labelChunks[row]!;
    dv.setUint32(base + NODE_COL_LABEL_OFF, labelCursor, true);
    dv.setUint32(base + NODE_COL_LABEL_LEN, chunk.length, true);
    labelView.set(chunk, labelCursor);
    labelCursor += chunk.length;
  });
  return buf;
}

// Build a VIEW-stream frame (camera+overlay+scene) with the Scene block filled — the live
// production shape (decodeViewFrame), replacing the deleted fd-3 SCENE frame fixture
// (decodeSnapshot, removed with Buffer.SnapshotState — per-owner-buffer-rows.md's final
// step). `scene` defaults to all-zero, i.e. "not yet populated".
function makeSceneSnapshot(scene?: SceneFields): ArrayBuffer {
  const total = BUF_VIEW_FRAME_HEADER_SIZE + CAMERA_STRIDE + OVERLAY_STRIDE + SCENE_STRIDE;
  const buf = new ArrayBuffer(total);
  const dv = new DataView(buf);
  const sceneOff = BUF_VIEW_FRAME_HEADER_SIZE + CAMERA_STRIDE + OVERLAY_STRIDE;
  if (scene) {
    dv.setFloat32(sceneOff + SCENE_COL_CX, scene.cx ?? 0, true);
    dv.setFloat32(sceneOff + SCENE_COL_CY, scene.cy ?? 0, true);
    dv.setFloat32(sceneOff + SCENE_COL_CZ, scene.cz ?? 0, true);
    dv.setFloat32(sceneOff + SCENE_COL_RADIUS, scene.radius ?? 0, true);
  }
  return buf;
}

describe("decodeNavNodes — row identity + buffer-sourced label", () => {
  it("maps buffer node row i to NavNode i with its geometry, selection, and label", () => {
    const buf = makeNodeFrame(2, [
      { cx: 1, cy: 2, cz: 3, radius: 10, sphereR: 40, selected: 0, label: "Alpha" },
      { cx: -5, cy: 6, cz: 7, radius: 12, sphereR: 0, selected: 1, label: "β-node" },
    ]);
    const decoded = decodeNodeFrame(buf)!;
    const nav = decodeNavNodes(decoded);

    expect(nav).toHaveLength(2);
    expect(nav[0]!.row).toBe(0);
    expect(nav[0]!.label).toBe("Alpha");
    expect(nav[0]!.center.x).toBeCloseTo(1, 5);
    expect(nav[0]!.center.y).toBeCloseTo(2, 5);
    expect(nav[0]!.center.z).toBeCloseTo(3, 5);
    expect(nav[0]!.radius).toBeCloseTo(10, 5);
    expect(nav[0]!.sphereR).toBeCloseTo(40, 5);
    expect(nav[0]!.selected).toBe(false);

    expect(nav[1]!.row).toBe(1);
    expect(nav[1]!.label).toBe("β-node"); // multi-byte rune round-trips
    expect(nav[1]!.selected).toBe(true);
    // sphereR 0 → undefined (old-path "missing" semantics)
    expect(nav[1]!.sphereR).toBeUndefined();
  });

  it("decodes an empty label as the empty string", () => {
    const buf = makeNodeFrame(1, [{ cx: 0, cy: 0, cz: 0, radius: 1, label: "" }]);
    const decoded = decodeNodeFrame(buf)!;
    const nav = decodeNavNodes(decoded);
    expect(nav[0]!.label).toBe("");
  });
});

describe("sceneSphereFromSnapshot", () => {
  it("falls back to origin/100 before the Scene block is populated (radius 0)", () => {
    const buf = makeSceneSnapshot();
    const decoded = decodeViewFrame(buf)!;
    const cs = sceneSphereFromSnapshot(decoded);
    expect(cs.center.x).toBe(0);
    expect(cs.center.y).toBe(0);
    expect(cs.center.z).toBe(0);
    expect(cs.radius).toBe(100);
  });

  it("reads the Go-owned Scene block center + radius verbatim (not derived from node centers)", () => {
    // Node centers (in a separate NODE frame, unused here) would be far from the scene
    // sphere center on purpose in the full system: sceneSphereFromSnapshot must read the
    // SCENE frame's Scene block, NOT compute a bbox centroid over node positions — proven
    // here simply by there being no node data in this SCENE-frame buffer at all.
    const buf = makeSceneSnapshot({ cx: 5, cy: 6, cz: 7, radius: 42 });
    const decoded = decodeViewFrame(buf)!;
    const cs = sceneSphereFromSnapshot(decoded);
    expect(cs.center.x).toBeCloseTo(5, 5);
    expect(cs.center.y).toBeCloseTo(6, 5);
    expect(cs.center.z).toBeCloseTo(7, 5);
    expect(cs.radius).toBeCloseTo(42, 5);
  });
});
