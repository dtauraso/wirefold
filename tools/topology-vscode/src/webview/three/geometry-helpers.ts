// geometry-helpers.ts — pure geometry/math functions for the 3D view.
// No React, no Three.js scene state — only computation.

import * as THREE from "three";
import type { RFNode, NodeData } from "../types";
import { NODE_DIM_FALLBACK } from "../state/node-dims";
import {
  CURVE_PARAM_NODE_RADIUS_DIVISOR,
} from "../../schema/curve-params";
import { getNodeGeometry } from "./node-geometry";

// Node/port positions are Go-authoritative: Go streams a node-geometry event per
// node (center + per-port world pos/dir) into useNodeGeometryStore, and these
// helpers READ that store. The local compute below is the pre-emit FALLBACK only —
// used when a node id is not yet in the store (startup race before the first emit).
// Go's nodeWorldPos / portDir mirror this fallback line-for-line (same RF y-down →
// Three y-up frame), so reading the store does not change the coordinate frame.

// ---------------------------------------------------------------------------
// Edge curve — REMOVED in Phase 3.
//
// The edge curve (wire-tube shape) is now Go-authoritative: Go holds node positions
// + per-edge control points and STREAMS them (geometry trace event); SingleEdgeTube
// draws the tube from Go's points (edge-geometry store). TS computes NO edge
// geometry — the former edge-curve builders were deleted (their names are now fenced
// by tools/check-ts-computes-no-geometry.sh).
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Node geometry
// ---------------------------------------------------------------------------

/**
 * Node body/ring sphere radius — reads Go's emitted radius, falls back to local
 * dims compute pre-emit. Go is authoritative: it streams radius on every
 * node-geometry event (min(w,h)/divisor); the local compute is the startup fallback
 * only (before Go's first emit), mirroring how nodeWorldPos falls back.
 */
export function nodeRadius(node: RFNode<NodeData>): number {
  const g = getNodeGeometry(node.id);
  if (g) return g.radius;
  return nodeRadiusLocal(node);
}

/** FALLBACK: local node-radius compute from node dims. Used pre-emit only. */
function nodeRadiusLocal(node: RFNode<NodeData>): number {
  return Math.min((node.data?.width ?? NODE_DIM_FALLBACK.width), (node.data?.height ?? NODE_DIM_FALLBACK.height)) / CURVE_PARAM_NODE_RADIUS_DIVISOR;
}

/**
 * AABB over node centers (Three y-up world frame). Reads each node's Go-emitted
 * center from the store; falls back to local compute for any node not yet emitted.
 */
export function boundingBox(nodes: RFNode<NodeData>[]) {
  if (nodes.length === 0) return { minX: -200, maxX: 200, minY: -200, maxY: 200 };
  let minX = Infinity, maxX = -Infinity, minY = Infinity, maxY = -Infinity;
  for (const n of nodes) {
    const c = nodeWorldPos(n);
    minX = Math.min(minX, c.x);
    maxX = Math.max(maxX, c.x);
    minY = Math.min(minY, c.y);
    maxY = Math.max(maxY, c.y);
  }
  return { minX, maxX, minY, maxY };
}

/**
 * 3D content sphere: bounding-box center + max-distance radius of all node world
 * positions, with a 10% margin.  This is the SINGLE shared source for "where is
 * the scene and how big is it?" used by computeContentSphere (interaction-controls)
 * and sceneCenter (interaction-handlers).  Returns radius ≥ 1.
 *
 * Does NOT include nodeRadius in the AABB; it uses raw centers.
 * Returns { center, radius } = (0,0,0), 100 when there are no finite nodes.
 */
export function contentSphere(nodes: RFNode<NodeData>[]): { center: THREE.Vector3; radius: number } {
  const center = new THREE.Vector3();
  if (!nodes || nodes.length === 0) return { center, radius: 100 };
  const min = new THREE.Vector3(Infinity, Infinity, Infinity);
  const max = new THREE.Vector3(-Infinity, -Infinity, -Infinity);
  let any = false;
  for (const n of nodes) {
    const p = nodeWorldPos(n);
    if (!Number.isFinite(p.x) || !Number.isFinite(p.y) || !Number.isFinite(p.z)) continue;
    min.min(p); max.max(p); any = true;
  }
  if (!any) return { center, radius: 100 };
  center.addVectors(min, max).multiplyScalar(0.5);
  let r = 0;
  for (const n of nodes) {
    const p = nodeWorldPos(n);
    if (!Number.isFinite(p.x) || !Number.isFinite(p.y) || !Number.isFinite(p.z)) continue;
    r = Math.max(r, p.distanceTo(center));
  }
  return { center, radius: Math.max(r * 1.1, 1) };
}

/**
 * World position for a node center — reads Go's emitted center, falls back to local
 * compute pre-emit. Go is authoritative for node position: on a node-move it updates
 * its held position AND re-emits node-geometry (see Wiring/stdin_reader.go
 * applyNodeMove → emitNodeGeometry), so the body follows the drag ~1 frame behind via
 * the stream rather than from local React Flow node.position. The local-compute path
 * is the startup fallback only (before Go's first emit).
 */
// `target`, when supplied, is written into and returned instead of allocating a
// fresh Vector3 — lets hot per-frame loops (LabelProjector) reuse scratch vectors.
// Omitting it preserves the original allocate-and-return behavior for all callers.
export function nodeWorldPos(node: RFNode<NodeData>, target?: THREE.Vector3): THREE.Vector3 {
  const g = getNodeGeometry(node.id);
  const out = target ?? new THREE.Vector3();
  if (g) return out.set(g.center.x, g.center.y, g.center.z);
  // FALLBACK (pre-emit ONLY, before Go's first node-geometry emit): authoritative
  // WORLD centers are Go-computed under the polar layout (polar roots → derived
  // Cartesian centers; see nodes/Wiring/derived.go) and streamed via node-geometry.
  // The editor does not replicate that derivation, so it returns the origin as a
  // stable placeholder until the stream arrives.
  return out.set(0, 0, 0);
}

// ---------------------------------------------------------------------------
// Port geometry
// ---------------------------------------------------------------------------

/**
 * Unit direction (z=0 plane) from node center toward the given port's position.
 * Go is authoritative: it streams per-port dir on every node-geometry event.
 * Returns null if the geometry has not arrived yet (pre-emit) or port not found.
 * Pre-emit callers (e.g. PortSphere) handle null by rendering nothing — port
 * spheres appear once Go's first emit lands, not before.  The old fallback that
 * mirrored Go's ring-anchor formula (N = floor(2πR/(d+p))) was drift and is
 * removed; Go is the only place that formula lives.
 */
export function portDir(node: RFNode<NodeData>, portName: string, isInput: boolean): THREE.Vector3 | null {
  const g = getNodeGeometry(node.id);
  if (!g) return null; // pre-emit placeholder: no port until Go streams geometry
  const p = g.ports.find((pp) => pp.name === portName && pp.isInput === isInput);
  if (!p) return null;
  return new THREE.Vector3(p.dir.x, p.dir.y, p.dir.z);
}

/** World position for the top of the node sphere (center.y + radius). */
export function nodeTopWorldPos(node: RFNode<NodeData>, target?: THREE.Vector3): THREE.Vector3 {
  const out = nodeWorldPos(node, target ?? new THREE.Vector3());
  out.y += nodeRadius(node);
  return out;
}

// The port-to-port curve builder was REMOVED in Phase 3. The port-to-port curve
// (wire-tube shape) is Go-authoritative now: Go computes the control points and
// streams them; the renderer draws from Go's points. portDir above
// remains — it places the PORT SPHERES (still used by node/port rendering), not the
// wire curve.

// ---------------------------------------------------------------------------
// Pulse + edge geometry — REMOVED (Phase 2 bead positions, Phase 3 edge curves).
//
// Bead position AND the wire-tube curve are both computed by Go and streamed to the
// renderer (MODEL.md: "the wire advances the bead and emits its position"; Go holds
// node positions + per-edge control points). TS plots only. The former per-bead
// arc-length / travel-time / pulse-speed math (Phase 2) and the edge-curve builders
// (Phase 3) were all deleted; nothing in the webview computes a wire curve or a bead
// position any more (enforced by tools/check-ts-computes-no-geometry.sh).
// ---------------------------------------------------------------------------

/**
 * Port-anchor ring projection (interaction input, NOT wire/bead geometry).
 * Given a node center and a world-space pointer hit on the node's ring plane,
 * return the node-center-relative anchor direction CONSTRAINED to the ring (z=0):
 * the in-plane (x,y) vector from center to hit, z zeroed. Magnitude is left as-is
 * (Go normalizes it); returns null if the hit lands exactly on the center (zero
 * in-plane direction) so the caller can ignore that frame.
 */
export function pointerRingAnchor(
  center: THREE.Vector3,
  hit: THREE.Vector3,
): { x: number; y: number; z: number } | null {
  const dx = hit.x - center.x;
  const dy = hit.y - center.y;
  if (dx === 0 && dy === 0) return null;
  return { x: dx, y: dy, z: 0 };
}

// ---------------------------------------------------------------------------
// Camera geometry
// ---------------------------------------------------------------------------

/**
 * 3D AABB over node centers ± radius (sphere extents), Three y-up world frame.
 * Used by HomeButton to frame the visible scene including node sphere radii.
 * Returns a unit-volume centered on the origin when nodes is empty.
 */
export function boundingBox3D(nodes: RFNode<NodeData>[]): {
  center: THREE.Vector3; sizeX: number; sizeY: number; sizeZ: number;
} {
  if (nodes.length === 0) {
    return { center: new THREE.Vector3(), sizeX: 400, sizeY: 400, sizeZ: 400 };
  }
  let minX = Infinity, maxX = -Infinity, minY = Infinity, maxY = -Infinity, minZ = Infinity, maxZ = -Infinity;
  for (const n of nodes) {
    const p = nodeWorldPos(n);
    const r = nodeRadius(n);
    minX = Math.min(minX, p.x - r); maxX = Math.max(maxX, p.x + r);
    minY = Math.min(minY, p.y - r); maxY = Math.max(maxY, p.y + r);
    minZ = Math.min(minZ, p.z - r); maxZ = Math.max(maxZ, p.z + r);
  }
  return {
    center: new THREE.Vector3((minX + maxX) / 2, (minY + maxY) / 2, (minZ + maxZ) / 2),
    sizeX: maxX - minX,
    sizeY: maxY - minY,
    sizeZ: maxZ - minZ,
  };
}

/**
 * Camera fit-distance: how far along +z to place the camera so a view of
 * `width` × `height` world-units fills the viewport at the given `fov` and
 * `aspect`.  Used by CameraFitter (scene-camera.tsx) and HomeButton
 * (camera-ui.tsx); both add their own margin on top of this base distance.
 *
 *   d = max(height/2, width/2/aspect) / tan(fov/2)
 *
 * where fov is in degrees and aspect = viewport-width / viewport-height.
 */
export function fitDistance(fovDeg: number, aspect: number, width: number, height: number): number {
  const fovRad = (fovDeg * Math.PI) / 180;
  const halfTan = Math.tan(fovRad / 2);
  return Math.max(height / 2, width / 2 / aspect) / halfTan;
}

// ---------------------------------------------------------------------------
// NDC ↔ pixel helpers
// ---------------------------------------------------------------------------

export function ndcToPixel(ndcX: number, ndcY: number, size: { width: number; height: number }): { px: number; py: number } {
  const px = (ndcX + 1) / 2 * size.width;
  const py = (1 - (ndcY + 1) / 2) * size.height;
  return { px, py };
}

export function pixelToNDC(clientX: number, clientY: number, rect: DOMRect): { ndcX: number; ndcY: number } {
  const ndcX = ((clientX - rect.left) / rect.width) * 2 - 1;
  const ndcY = -((clientY - rect.top) / rect.height) * 2 + 1;
  return { ndcX, ndcY };
}
