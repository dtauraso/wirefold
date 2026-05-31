// geometry-helpers.ts — pure geometry/math functions for the 3D view.
// No React, no Three.js scene state — only computation.

import * as THREE from "three";
import type { RFNode, NodeData } from "../types";
import {
  CURVE_PARAM_PULSE_SPEED_WU_PER_MS,
  CURVE_PARAM_MIN_ARC_LENGTH,
  CURVE_PARAM_BULGE_FACTOR,
  CURVE_PARAM_BEZIER_SAMPLE_COUNT,
} from "../../schema/curve-params";

// ---------------------------------------------------------------------------
// Edge curve
// ---------------------------------------------------------------------------

/**
 * Returns the center of `node` in world space.
 * `other` is accepted but ignored — kept for API symmetry with scene-content.tsx (kept in sync).
 */
function surfacePointForNodes(node: RFNode<NodeData>, _other: RFNode<NodeData>): THREE.Vector3 {
  return nodeWorldPos(node);
}

/**
 * Build the QuadraticBezierCurve3 for an edge between two nodes.
 * Control-point math matches SingleEdgeTube's useMemo exactly.
 * Called from moveNode (synchronous, same drag tick) and from initial load / createEdge.
 */
export function buildEdgeCurve(
  src: RFNode<NodeData>,
  tgt: RFNode<NodeData>,
): THREE.QuadraticBezierCurve3 {
  const p0 = surfacePointForNodes(src, tgt);
  const p2 = surfacePointForNodes(tgt, src);
  const mid = p0.clone().add(p2).multiplyScalar(0.5);
  const edgeDir = p2.clone().sub(p0).normalize();
  const lift = new THREE.Vector3(0, 0, 1).cross(edgeDir).normalize();
  const span = p0.distanceTo(p2);
  const p1 = mid.clone().addScaledVector(lift, span * CURVE_PARAM_BULGE_FACTOR);
  return new THREE.QuadraticBezierCurve3(p0, p1, p2);
}

// ---------------------------------------------------------------------------
// Node geometry
// ---------------------------------------------------------------------------

/** Node sphere radius from node dimensions. */
export function nodeRadius(node: RFNode<NodeData>): number {
  return Math.min((node.data?.width ?? 110), (node.data?.height ?? 60)) / 4;
}

export function boundingBox(nodes: RFNode<NodeData>[]) {
  if (nodes.length === 0) return { minX: -200, maxX: 200, minY: -200, maxY: 200 };
  let minX = Infinity, maxX = -Infinity, minY = Infinity, maxY = -Infinity;
  for (const n of nodes) {
    const w = (n.data?.width ?? 110) / 2;
    const h = (n.data?.height ?? 60) / 2;
    minX = Math.min(minX, n.position.x - w);
    maxX = Math.max(maxX, n.position.x + w);
    minY = Math.min(minY, n.position.y - h);
    maxY = Math.max(maxY, n.position.y + h);
  }
  return { minX, maxX, minY, maxY };
}

/** World position for a node center (RF y-down → Three y-up). */
export function nodeWorldPos(node: RFNode<NodeData>): THREE.Vector3 {
  const x = node.position.x + (node.data?.width ?? 110) / 2;
  const y = -(node.position.y + (node.data?.height ?? 60) / 2);
  return new THREE.Vector3(x, y, 0);
}

// ---------------------------------------------------------------------------
// Port geometry
// ---------------------------------------------------------------------------

const SLOT_PCT = [25, 50, 75] as const;

/**
 * Unit direction (z=0 plane) from node center toward the given port's position,
 * derived from side/slot like the 2D handle layout. Returns null if port not found.
 */
export function portDir(node: RFNode<NodeData>, portName: string, isInput: boolean): THREE.Vector3 | null {
  const list = (isInput ? node.data?.inputs : node.data?.outputs) ?? [];
  const idx = list.findIndex((p) => p.name === portName);
  if (idx < 0) return null;
  const port = list[idx];
  const side = port.side ?? (isInput ? "left" : "right");
  // ports sharing this resolved side, in list order:
  const sameSide = list.filter((p) => (p.side ?? (isInput ? "left" : "right")) === side);
  const onSideIdx = sameSide.findIndex((p) => p === port);
  const pct = port.slot !== undefined ? SLOT_PCT[port.slot] : ((onSideIdx + 1) * 100) / (sameSide.length + 1);
  const w = node.data?.width ?? 110, h = node.data?.height ?? 60;
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

/**
 * World-space port position on the node sphere surface, or node center if no port.
 * Uses the sphere surface point in the direction of the port, so endpoints sit
 * on the sphere rather than at the center.
 */
export function portWorldPos(node: RFNode<NodeData>, portName: string | null | undefined, isInput: boolean): THREE.Vector3 {
  const center = nodeWorldPos(node);
  if (!portName) return center;
  const dir = portDir(node, portName, isInput);
  if (!dir) return center;
  return center.clone().add(dir.multiplyScalar(nodeRadius(node)));
}

/** World position for the top of the node sphere (center.y + radius). */
export function nodeTopWorldPos(node: RFNode<NodeData>): THREE.Vector3 {
  const center = nodeWorldPos(node);
  const r = nodeRadius(node);
  return new THREE.Vector3(center.x, center.y + r, center.z);
}

/**
 * Scene center (centroid of node bounding box), used for dolly distance.
 * Falls back to origin when no nodes.
 */
export function sceneCenter(nodes: RFNode<NodeData>[]): THREE.Vector3 {
  if (nodes.length === 0) return new THREE.Vector3(0, 0, 0);
  const { minX, maxX, minY, maxY } = boundingBox(nodes);
  return new THREE.Vector3((minX + maxX) / 2, -(minY + maxY) / 2, 0);
}

// ---------------------------------------------------------------------------
// Pulse geometry
// ---------------------------------------------------------------------------

/**
 * Uniform pulse speed — single source of truth in nodes/Wiring/curve_params.go.
 * Re-exported for callers that reference PULSE_SPEED_WU_PER_MS directly.
 */
export const PULSE_SPEED_WU_PER_MS = CURVE_PARAM_PULSE_SPEED_WU_PER_MS;

/**
 * Quadratic Bezier arc length between two RF node-center positions.
 * Mirrors Go's BezierArcLength in nodes/Wiring/curve_params.go exactly:
 *   - control point = chord midpoint offset by BULGE_FACTOR * chordLen perpendicularly
 *   - arc integrated with BEZIER_SAMPLE_COUNT equal-parameter segments
 *   - result floored at MIN_ARC_LENGTH
 * Using node centers (not surface points) keeps Go and TS inputs identical.
 */
export function rfArcLength(ax: number, ay: number, bx: number, by: number): number {
  const chordX = bx - ax;
  const chordY = by - ay;
  const chordLen = Math.sqrt(chordX * chordX + chordY * chordY);

  // Midpoint of chord.
  const midX = (ax + bx) * 0.5;
  const midY = (ay + by) * 0.5;

  // Perpendicular: rotate chord direction 90° CCW and normalise.
  let perpX = 0, perpY = 0;
  if (chordLen > 0) {
    perpX = -chordY / chordLen;
    perpY = chordX / chordLen;
  }

  // Control point p1.
  const p1x = midX + perpX * CURVE_PARAM_BULGE_FACTOR * chordLen;
  const p1y = midY + perpY * CURVE_PARAM_BULGE_FACTOR * chordLen;

  // Integrate arc length over BEZIER_SAMPLE_COUNT segments.
  const n = CURVE_PARAM_BEZIER_SAMPLE_COUNT;
  const inv = 1.0 / n;
  let prevX = ax, prevY = ay;
  let total = 0;
  for (let i = 1; i <= n; i++) {
    const t = i * inv;
    const u = 1 - t;
    const bpx = u * u * ax + 2 * u * t * p1x + t * t * bx;
    const bpy = u * u * ay + 2 * u * t * p1y + t * t * by;
    const dx = bpx - prevX;
    const dy = bpy - prevY;
    total += Math.sqrt(dx * dx + dy * dy);
    prevX = bpx;
    prevY = bpy;
  }

  return total < CURVE_PARAM_MIN_ARC_LENGTH ? CURVE_PARAM_MIN_ARC_LENGTH : total;
}

/** Convert arc length to simLatencyMs using the uniform pulse speed. */
export function arcLengthToSimLatencyMs(arcLength: number): number {
  return arcLength / CURVE_PARAM_PULSE_SPEED_WU_PER_MS;
}

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

/**
 * World-units-per-pixel for panning in the camera's screen plane.
 * Computed from the perpendicular distance to the content plane and the camera FOV.
 */
export function worldPerPixel(cam: THREE.PerspectiveCamera, canvasH: number): number {
  const d = camToPlaneDistance(cam);
  const fovRad = (cam.fov * Math.PI) / 180;
  return (2 * d * Math.tan(fovRad / 2)) / canvasH;
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
