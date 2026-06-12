// geometry-helpers.ts — pure geometry/math functions for the 3D view.
// No React, no Three.js scene state — only computation.

import * as THREE from "three";
import type { RFNode, NodeData } from "../types";
import { NODE_DIM_FALLBACK } from "../state/node-dims";
import {
  CURVE_PARAM_NODE_RADIUS_DIVISOR,
  CURVE_PARAM_SLOT_PCT0,
  CURVE_PARAM_SLOT_PCT1,
  CURVE_PARAM_SLOT_PCT2,
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
 * World position for a node center — reads Go's emitted center, falls back to local
 * compute pre-emit. Go is authoritative for node position: on a node-move it updates
 * its held position AND re-emits node-geometry (see Wiring/stdin_reader.go
 * applyNodeMove → emitNodeGeometry), so the body follows the drag ~1 frame behind via
 * the stream rather than from local React Flow node.position. The local-compute path
 * is the startup fallback only (before Go's first emit).
 */
export function nodeWorldPos(node: RFNode<NodeData>): THREE.Vector3 {
  const g = getNodeGeometry(node.id);
  if (g) return new THREE.Vector3(g.center.x, g.center.y, g.center.z);
  return nodeWorldPosLocal(node);
}

/** FALLBACK: local node-center compute (RF y-down → Three y-up). Used pre-emit only. */
function nodeWorldPosLocal(node: RFNode<NodeData>): THREE.Vector3 {
  const x = node.position.x + (node.data?.width ?? NODE_DIM_FALLBACK.width) / 2;
  const y = -(node.position.y + (node.data?.height ?? NODE_DIM_FALLBACK.height) / 2);
  return new THREE.Vector3(x, y, 0);
}

// ---------------------------------------------------------------------------
// Port geometry
// ---------------------------------------------------------------------------

const SLOT_PCT = [CURVE_PARAM_SLOT_PCT0, CURVE_PARAM_SLOT_PCT1, CURVE_PARAM_SLOT_PCT2] as const;

/**
 * Unit direction (z=0 plane) from node center toward the given port's position,
 * derived from side/slot like the 2D handle layout. Returns null if port not found.
 */
export function portDir(node: RFNode<NodeData>, portName: string, isInput: boolean): THREE.Vector3 | null {
  const g = getNodeGeometry(node.id);
  if (g) {
    const p = g.ports.find((pp) => pp.name === portName && pp.isInput === isInput);
    if (p) return new THREE.Vector3(p.dir.x, p.dir.y, p.dir.z);
    return null;
  }
  return portDirLocal(node, portName, isInput);
}

/**
 * FALLBACK: local port-direction compute. Used pre-emit only.
 * Unit direction (z=0 plane) from node center toward the given port's position,
 * derived from side/slot like the 2D handle layout. Returns null if port not found.
 */
function portDirLocal(node: RFNode<NodeData>, portName: string, isInput: boolean): THREE.Vector3 | null {
  const list = (isInput ? node.data?.inputs : node.data?.outputs) ?? [];
  const idx = list.findIndex((p) => p.name === portName);
  if (idx < 0) return null;
  const port = list[idx];
  const side = port.side ?? (isInput ? "left" : "right");
  // ports sharing this resolved side, in list order:
  const sameSide = list.filter((p) => (p.side ?? (isInput ? "left" : "right")) === side);
  const onSideIdx = sameSide.findIndex((p) => p === port);
  const pct = port.slot !== undefined ? SLOT_PCT[port.slot] : ((onSideIdx + 1) * 100) / (sameSide.length + 1);
  const w = node.data?.width ?? NODE_DIM_FALLBACK.width, h = node.data?.height ?? NODE_DIM_FALLBACK.height;
  // local border point offset from center (y-up): pct measured from top for left/right, from left for top/bottom
  let bx = 0, by = 0;
  if (side === "left")        { bx = -w / 2; by = h * (0.5 - pct / 100); }
  else if (side === "right")  { bx =  w / 2; by = h * (0.5 - pct / 100); }
  else if (side === "top")    { by =  h / 2; bx = w * (pct / 100 - 0.5); }
  else                        { by = -h / 2; bx = w * (pct / 100 - 0.5); } // bottom
  const dir = new THREE.Vector3(bx, by, 0);
  if (dir.lengthSq() === 0) {
    // exact center fallback: cardinal by side
    if (side === "left")       dir.set(-1, 0, 0);
    else if (side === "right") dir.set( 1, 0, 0);
    else if (side === "top")   dir.set( 0, 1, 0);
    else                       dir.set( 0,-1, 0);
  }
  return dir.normalize();
}

/** World position for the top of the node sphere (center.y + radius). */
export function nodeTopWorldPos(node: RFNode<NodeData>): THREE.Vector3 {
  const center = nodeWorldPos(node);
  const r = nodeRadius(node);
  return new THREE.Vector3(center.x, center.y + r, center.z);
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

// ---------------------------------------------------------------------------
// Camera geometry
// ---------------------------------------------------------------------------

/**
 * True perpendicular distance from camera to the z=0 plane (content plane).
 * This is the projection of the camera-to-origin vector onto the camera forward
 * direction, computed as: |cam.position · viewDir| where viewDir is the camera
 * forward in world space. Correct after arbitrary rotation (not just looking down -Z).
 * Clamped to a minimum of 10 to avoid zero/negative values.
 */
export function camToPlaneDistance(cam: THREE.PerspectiveCamera): number {
  // Camera forward in world space (points away from camera, into scene).
  const forward = new THREE.Vector3(0, 0, -1).applyQuaternion(cam.quaternion);
  // Distance = projection of camera position onto the (negated) forward vector.
  // The z=0 plane has normal (0,0,1). Distance = |cam.position · (0,0,1)| = |cam.position.z|
  // when looking straight down. After rotation, use the component of the camera
  // position along the view axis (how far back the camera is from z=0 along view).
  // More precisely: distance from cam to the plane along the view ray.
  // Ray: origin=cam.position, dir=forward. Plane: z=0, normal=(0,0,1).
  // t = -cam.position.z / forward.z  (ray-plane intersection param)
  // If forward.z ≈ 0 (camera looking sideways), fall back to |cam.position.z|.
  const fwdZ = forward.z;
  if (Math.abs(fwdZ) > 0.01) {
    return Math.max(Math.abs(-cam.position.z / fwdZ), 10);
  }
  return Math.max(Math.abs(cam.position.z), 10);
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
