// Unit tests for buffer-nav.ts — the id table + pure decodeNavNodes + content sphere.
//
// Builds raw snapshot ArrayBuffers matching the Go node-block layout and asserts:
//   - the ordered id table records first-seen ids and ignores repeats
//   - decodeNavNodes pairs buffer row i with id-table entry i (ordering guarantee)
//   - sphereR==0 decodes to undefined (old-path "missing" semantics)
//   - contentSphereFromCenters matches the geometry-helpers.contentSphere formula

import { describe, it, expect, beforeEach } from "vitest";
import * as THREE from "three";
import { decodeSnapshot } from "../src/webview/three/buffer-decode";
import {
  recordNavNodeId, recordNavNodeLabel, clearNavNodeIds, getNavNodeIds,
  getNavNodeLabel, getNavNodeKind,
  decodeNavNodes, contentSphereFromCenters, instanceIdToNodeId,
} from "../src/webview/three/buffer-nav";
import { NODE_DEFS } from "../src/schema/node-defs";
import {
  BUF_HEADER_SIZE, NODE_STRIDE, INTERIOR_STRIDE, CAMERA_STRIDE, OVERLAY_STRIDE,
  NODE_COL_CX, NODE_COL_CY, NODE_COL_CZ, NODE_COL_RADIUS,
  NODE_COL_SPHERE_R, NODE_COL_SELECTED,
} from "../src/schema/buffer-layout";
import { INTERIOR_SLOTS_PER_NODE } from "../src/webview/three/buffer-decode";

// Build a snapshot with `nodeCount` node rows (no beads/edges). Returns a setter to
// fill node fields by row.
function makeNodeSnapshot(nodeCount: number): { buf: ArrayBuffer; setNode: (row: number, f: {
  cx?: number; cy?: number; cz?: number; radius?: number; sphereR?: number; selected?: number;
}) => void } {
  const nodeBytes = nodeCount * NODE_STRIDE;
  // Interior block (fixed INTERIOR_SLOTS_PER_NODE rows per node) sits between the node
  // and camera blocks; decodeSnapshot's length check requires it even when empty.
  const interiorBytes = nodeCount * INTERIOR_SLOTS_PER_NODE * INTERIOR_STRIDE;
  const total = BUF_HEADER_SIZE + nodeBytes + interiorBytes + CAMERA_STRIDE + OVERLAY_STRIDE;
  const buf = new ArrayBuffer(total);
  const dv = new DataView(buf);
  dv.setUint32(8, nodeCount, true); // nodeCount header field
  const nodeOff = BUF_HEADER_SIZE;
  const setNode = (row: number, f: { cx?: number; cy?: number; cz?: number; radius?: number; sphereR?: number; selected?: number }) => {
    const base = nodeOff + row * NODE_STRIDE;
    if (f.cx !== undefined) dv.setFloat32(base + NODE_COL_CX, f.cx, true);
    if (f.cy !== undefined) dv.setFloat32(base + NODE_COL_CY, f.cy, true);
    if (f.cz !== undefined) dv.setFloat32(base + NODE_COL_CZ, f.cz, true);
    if (f.radius !== undefined) dv.setFloat32(base + NODE_COL_RADIUS, f.radius, true);
    if (f.sphereR !== undefined) dv.setFloat32(base + NODE_COL_SPHERE_R, f.sphereR, true);
    if (f.selected !== undefined) dv.setUint8(base + NODE_COL_SELECTED, f.selected);
  };
  return { buf, setNode };
}

describe("buffer-nav id table", () => {
  beforeEach(() => clearNavNodeIds());

  it("records ids in first-seen order and ignores repeats", () => {
    recordNavNodeId("a");
    recordNavNodeId("b");
    recordNavNodeId("a"); // repeat (node re-emit on move) → ignored
    recordNavNodeId("c");
    expect(getNavNodeIds()).toEqual(["a", "b", "c"]);
  });

  it("clearNavNodeIds resets the table", () => {
    recordNavNodeId("x");
    clearNavNodeIds();
    expect(getNavNodeIds()).toEqual([]);
  });
});

describe("node-label sidecar — label + kind by id, row order preserved", () => {
  beforeEach(() => clearNavNodeIds());

  it("records label and kind together, appending in first-seen order", () => {
    recordNavNodeLabel("a", "Alpha", "Hold");
    recordNavNodeLabel("b", "Beta", "Pacer");
    recordNavNodeLabel("a", "Alpha2", "Hold"); // repeat id → not reordered, label updated
    expect(getNavNodeIds()).toEqual(["a", "b"]);
    expect(getNavNodeLabel("a")).toBe("Alpha2");
    expect(getNavNodeKind("a")).toBe("Hold");
    expect(getNavNodeKind("b")).toBe("Pacer");
  });

  it("leaves kind undefined when the sidecar omits it (empty string)", () => {
    recordNavNodeLabel("c", "Gamma", "");
    expect(getNavNodeKind("c")).toBeUndefined();
    expect(getNavNodeLabel("c")).toBe("Gamma");
  });

  it("maps a node's kind to its NODE_DEFS fill/stroke (kind→color lookup)", () => {
    recordNavNodeLabel("n", "N", "Hold");
    const kind = getNavNodeKind("n")!;
    const def = NODE_DEFS[kind]!;
    // The render path uses exactly this lookup; assert the def carries fill+stroke.
    expect(def.fill).toBe("#f3e5f5");
    expect(def.stroke).toBe("#6a1b9a");
  });

  it("clearNavNodeIds also clears the kind map", () => {
    recordNavNodeLabel("z", "Z", "Pulse");
    clearNavNodeIds();
    expect(getNavNodeKind("z")).toBeUndefined();
  });
});

describe("decodeNavNodes — row ↔ id pairing (ordering guarantee)", () => {
  beforeEach(() => clearNavNodeIds());

  it("pairs buffer node row i with id-table entry i", () => {
    // Same first-seen order as the buffer rows below.
    recordNavNodeId("n0");
    recordNavNodeId("n1");

    const { buf, setNode } = makeNodeSnapshot(2);
    setNode(0, { cx: 1, cy: 2, cz: 3, radius: 10, sphereR: 40, selected: 0 });
    setNode(1, { cx: -5, cy: 6, cz: 7, radius: 12, sphereR: 0, selected: 1 });

    const decoded = decodeSnapshot(buf)!;
    const nav = decodeNavNodes(decoded, getNavNodeIds());

    expect(nav).toHaveLength(2);
    expect(nav[0]!.id).toBe("n0");
    expect(nav[0]!.center.x).toBeCloseTo(1, 5);
    expect(nav[0]!.center.y).toBeCloseTo(2, 5);
    expect(nav[0]!.center.z).toBeCloseTo(3, 5);
    expect(nav[0]!.radius).toBeCloseTo(10, 5);
    expect(nav[0]!.sphereR).toBeCloseTo(40, 5);
    expect(nav[0]!.selected).toBe(false);

    expect(nav[1]!.id).toBe("n1");
    expect(nav[1]!.selected).toBe(true);
    // sphereR 0 → undefined (old-path "missing" semantics)
    expect(nav[1]!.sphereR).toBeUndefined();
  });

  it("falls back to a synthetic #i id when the id table is short", () => {
    recordNavNodeId("only0");
    const { buf, setNode } = makeNodeSnapshot(2);
    setNode(0, { cx: 0, cy: 0, cz: 0, radius: 1 });
    setNode(1, { cx: 0, cy: 0, cz: 0, radius: 1 });
    const decoded = decodeSnapshot(buf)!;
    const nav = decodeNavNodes(decoded, getNavNodeIds());
    expect(nav[0]!.id).toBe("only0");
    expect(nav[1]!.id).toBe("#1");
  });
});

describe("instanceIdToNodeId — InstancedMesh instanceId ↔ node id", () => {
  // This is the hit-testing fix: under the new system nodes are a buffer InstancedMesh
  // with no per-node userData.nodeId, so RaycasterHelper resolves a node hit from the
  // ray's instanceId via this mapping (instanceId == buffer node row == id-table index).
  it("maps instanceId to the row-aligned node id", () => {
    const ids = ["n0", "n1", "n2"];
    expect(instanceIdToNodeId(0, ids)).toBe("n0");
    expect(instanceIdToNodeId(1, ids)).toBe("n1");
    expect(instanceIdToNodeId(2, ids)).toBe("n2");
  });

  it("returns null for an out-of-range instanceId (id table not yet populated)", () => {
    expect(instanceIdToNodeId(3, ["n0", "n1", "n2"])).toBeNull();
    expect(instanceIdToNodeId(0, [])).toBeNull();
  });
});

describe("contentSphereFromCenters", () => {
  it("returns origin/100 for no centers", () => {
    const cs = contentSphereFromCenters([]);
    expect(cs.center.equals(new THREE.Vector3())).toBe(true);
    expect(cs.radius).toBe(100);
  });

  it("computes bbox center + farthest-node radius with 10% margin", () => {
    const centers = [
      new THREE.Vector3(-10, 0, 0),
      new THREE.Vector3(10, 0, 0),
    ];
    const cs = contentSphereFromCenters(centers);
    expect(cs.center.x).toBeCloseTo(0, 5);
    // farthest = 10 from center; ×1.1 = 11
    expect(cs.radius).toBeCloseTo(11, 5);
  });

  it("radius is at least 1 for a single center", () => {
    const cs = contentSphereFromCenters([new THREE.Vector3(3, 4, 5)]);
    expect(cs.center.x).toBeCloseTo(3, 5);
    expect(cs.radius).toBe(1);
  });

  it("ignores non-finite centers", () => {
    const cs = contentSphereFromCenters([
      new THREE.Vector3(0, 0, 0),
      new THREE.Vector3(NaN, 0, 0),
      new THREE.Vector3(6, 0, 0),
    ]);
    expect(cs.center.x).toBeCloseTo(3, 5);
    expect(cs.radius).toBeCloseTo(3.3, 5);
  });
});
