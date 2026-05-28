// geometry-helpers.ts — pure geometry/math functions for the 3D view.
// No React, no Three.js scene state — only computation.

import * as THREE from "three";
import type { RFNode, NodeData } from "../types";

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
 * Uniform pulse speed — must match Go's nodes/Wiring/paced_wire.go:PulseSpeedWuPerMs.
 * Both sides derive simLatencyMs = arcLength / PULSE_SPEED_WU_PER_MS.
 */
export const PULSE_SPEED_WU_PER_MS = 0.08;

/** Minimum arc length, matching Go's loader.go:minArcLength. Prevents zero-duration pulses. */
const MIN_ARC_LENGTH = 1.0;

/**
 * Straight-line distance between two RF positions (matching Go's arcLengthBetween).
 * Uses raw RF position coords — same coordinate space Go uses for specPosition.
 */
export function rfArcLength(ax: number, ay: number, bx: number, by: number): number {
  const dx = bx - ax;
  const dy = by - ay;
  const d = Math.sqrt(dx * dx + dy * dy);
  return d < MIN_ARC_LENGTH ? MIN_ARC_LENGTH : d;
}

/** Convert arc length to simLatencyMs using the uniform pulse speed. */
export function arcLengthToSimLatencyMs(arcLength: number): number {
  return arcLength / PULSE_SPEED_WU_PER_MS;
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
