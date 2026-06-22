// viewpoint-bridge.ts — fire-and-forget helpers for sending polar-camera viewpoint
// edits to Go via the TS→Go bridge. No await, no Promise, no delivery signal.
// See CLAUDE.md §Bridge surface.

import * as THREE from "three";
import { vscode } from "../vscode-api";

// ---------------------------------------------------------------------------
// Frame-convention helpers (Go uses pole = +y)
// ---------------------------------------------------------------------------

/**
 * Convert a world unit direction vector to Go polar angles.
 * theta = acos(clamp(y, -1, 1))  (angle from +y pole)
 * phi   = atan2(z, x)            (longitude, x=0 axis)
 */
export function worldDirToAngles(v: THREE.Vector3): [number, number] {
  const theta = Math.acos(Math.max(-1, Math.min(1, v.y)));
  const phi = Math.atan2(v.z, v.x);
  return [theta, phi];
}

/**
 * Convert Go polar angles + radius to a world-space offset vector.
 * x = r*sin(theta)*cos(phi)
 * y = r*cos(theta)
 * z = r*sin(theta)*sin(phi)
 */
export function anglesToWorldOffset(r: number, theta: number, phi: number): THREE.Vector3 {
  const sinTheta = Math.sin(theta);
  return new THREE.Vector3(
    r * sinTheta * Math.cos(phi),
    r * Math.cos(theta),
    r * sinTheta * Math.sin(phi),
  );
}

/** Tell Go to set camera state (all fields optional; omitted fields are unchanged). */
export function sendViewpointSet(
  pivot: [number, number, number],
  r: number,
  pos: [number, number],
  up: [number, number],
): void {
  const [pivotX, pivotY, pivotZ] = pivot;
  const [posTheta, posPhi] = pos;
  const [upTheta, upPhi] = up;
  vscode.postMessage({
    type: "edit",
    op: "viewpoint",
    viewpoint: { kind: "set", pivotX, pivotY, pivotZ, r, posTheta, posPhi, upTheta, upPhi },
  });
}

/** Tell Go to orbit from one spherical position to another. */
export function sendViewpointOrbit(
  from: [number, number],
  to: [number, number],
): void {
  const [fromTheta, fromPhi] = from;
  const [toTheta, toPhi] = to;
  vscode.postMessage({
    type: "edit",
    op: "viewpoint",
    viewpoint: { kind: "orbit", fromTheta, fromPhi, toTheta, toPhi },
  });
}

/** Tell Go to zoom by a multiplicative factor. */
export function sendViewpointZoom(factor: number): void {
  vscode.postMessage({
    type: "edit",
    op: "viewpoint",
    viewpoint: { kind: "zoom", factor },
  });
}

/** Tell Go to pan the pivot point by a world-space delta. */
export function sendViewpointPan(dx: number, dy: number, dz: number): void {
  vscode.postMessage({
    type: "edit",
    op: "viewpoint",
    viewpoint: { kind: "pan", dx, dy, dz },
  });
}
