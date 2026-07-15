// polar.ts — world-direction → Go-convention polar angles.
//
// Go's gesture FSM (nodes/Wiring/gesture.go) owns all navigation math (orbit / zoom / pan);
// there is no navigation logic in TS. What remains here is a single conversion: a world
// direction vector → [theta, phi] in Go's pole-frame convention, used by viewpoint-bridge.ts
// (BufferCamera placement) and NavGuides.tsx (drawing angle labels/handholds from Go-owned
// positions). This is presentation math over Go-owned data, not gesture/navigation logic.

import * as THREE from "three";

/** Sphere + an orthonormal frame: pole and two equatorial reference axes. */
export interface PolarFrame {
  center: THREE.Vector3;
  radius: number;
  pole: THREE.Vector3;
  refX: THREE.Vector3; // azimuth θ = 0
  refY: THREE.Vector3; // azimuth θ = 90°
}

/** A canonical PolarFrame for the Go/world-Y-pole convention:
 *  pole = +y, refX = +x, refY = +z.
 *  theta (first return) = colatitude from +y (0 = north, π/2 = equator, π = south)
 *  phi   (second return) = azimuth in the x-z plane, 0 at +x */
export const Y_POLE_FRAME: PolarFrame = {
  center: new THREE.Vector3(0, 0, 0),
  radius: 1,
  pole: new THREE.Vector3(0, 1, 0),
  refX: new THREE.Vector3(1, 0, 0),
  refY: new THREE.Vector3(0, 0, 1),
};

/** Convert a world direction (need not be unit length) to [theta, phi] using the
 *  frame's pole/refX/refY.  This is the SINGLE authoritative world-dir→angles
 *  conversion used by viewpoint-bridge and NavGuides.
 *    theta = acos(d·pole)           — colatitude from pole (0…π)
 *    phi   = atan2(d·refY, d·refX) — azimuth around pole  (-π…π)
 *  Pass Y_POLE_FRAME for the Go/world-Y convention. */
export function worldDirToFrameAngles(v: THREE.Vector3, f: PolarFrame): [number, number] {
  const d = v.clone().normalize();
  const theta = Math.acos(THREE.MathUtils.clamp(d.dot(f.pole), -1, 1));
  const phi = Math.atan2(d.dot(f.refY), d.dot(f.refX));
  return [theta, phi];
}
