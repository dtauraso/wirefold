// polar.ts — polar/spherical primitives for navigation geometry.
//
// The model is POLAR: a direction on the sphere is an angle pair, not an (x,y,z) vector, and
// operations are angle arithmetic. Cartesian is quarantined to cameraFrame/toWorld/planeSlide
// (edge helpers). Navigation logic imports only this file, so there is no cross product or world
// vector in reach to "sneak in" — and no cross-product degeneracy.

import * as THREE from "three";

/** A direction on the sphere, polar about a frame's pole.
 *  phi: angle FROM the pole (0 = pole, π/2 = equator, π = opposite pole).
 *  theta: azimuth AROUND the pole. */
export interface Polar {
  theta: number;
  phi: number;
}

/** Sphere + an orthonormal frame: pole and two equatorial reference axes. The two refs are the
 *  only Cartesian basis formed in the whole system, and only here (hidden plumbing). */
export interface PolarFrame {
  center: THREE.Vector3;
  radius: number;
  pole: THREE.Vector3;
  refX: THREE.Vector3; // azimuth θ = 0
  refY: THREE.Vector3; // azimuth θ = 90°
}

/** Build a PolarFrame aligned to a camera's screen basis: pole = toward the camera (its +Z),
 *  refX = screen right (camera +X), refY = screen up (camera +Y). This is the frame the cursor
 *  is read in, so screen direction maps 1:1 to azimuth and the grab stays under the cursor on the
 *  FRONT of the sphere. The only Cartesian (quaternion basis extraction) is quarantined here. */
export function cameraFrame(camQuat: THREE.Quaternion, center: THREE.Vector3, radius: number): PolarFrame {
  const pole = new THREE.Vector3(0, 0, 1).applyQuaternion(camQuat); // toward the camera
  const refX = new THREE.Vector3(1, 0, 0).applyQuaternion(camQuat); // screen right
  const refY = new THREE.Vector3(0, 1, 0).applyQuaternion(camQuat); // screen up
  return { center, radius, pole, refX, refY };
}

/** The equatorial unit direction at azimuth θ (cos θ·refX + sin θ·refY). A pure angle → axis.
 *  Internal to the toolkit (used by toWorld). */
function equatorDir(f: PolarFrame, theta: number): THREE.Vector3 {
  return f.refX.clone().multiplyScalar(Math.cos(theta)).add(f.refY.clone().multiplyScalar(Math.sin(theta)));
}

/** Polar → world point on the sphere (edge conversion, for rendering only). */
export function toWorld(f: PolarFrame, q: Polar): THREE.Vector3 {
  const s = Math.sin(q.phi);
  const dir = f.pole.clone().multiplyScalar(Math.cos(q.phi)).add(equatorDir(f, q.theta).multiplyScalar(s));
  return f.center.clone().add(dir.multiplyScalar(f.radius));
}

/** Input edge: a Cartesian screen delta (wheel/pointer px) → polar (r, angle). The mirror of
 *  toWorld — the only place raw (dx, dy) is read for a gesture; navigation uses (r, angle).
 *  angle is measured in the screen/equatorial plane (atan2(dy, dx)); r is the magnitude. */
export function deltaToPolar(dx: number, dy: number): { r: number; angle: number } {
  return { r: Math.hypot(dx, dy), angle: Math.atan2(dy, dx) };
}

/** World point on/near the sphere → polar (θ, φ) about the frame. Edge conversion for input
 *  (e.g. the cursor's raycast point), the mirror of toWorld. */
export function fromWorld(f: PolarFrame, worldPoint: THREE.Vector3): Polar {
  const d = worldPoint.clone().sub(f.center).normalize();
  const phi = Math.acos(THREE.MathUtils.clamp(d.dot(f.pole), -1, 1));
  const theta = Math.atan2(d.dot(f.refY), d.dot(f.refX));
  return { theta, phi };
}

/** Cursor input edge: screen-pixel delta from sphere center → Polar.
 *  ONE uniform rule for the whole sphere — no clamp, no rim, no front/back special-casing
 *  (a sphere is uniform; there is nothing to protect against). phi = ρ/scale grows linearly
 *  and UNBOUNDED with screen distance: 0 at the center (tumble), past π/2 it rolls and then
 *  continues smoothly to the far side and around. `scale` is the sphere's on-screen pixel
 *  radius Rpx, so near the center this matches the true sphere projection exactly.
 *  theta = atan2(-dy,dx) — direction around center maps to azimuth. dy is negated because
 *  refY points screen-UP but client/screen Y increases downward. Pure angle arithmetic. */
export function screenToPolar(dxFromCenter: number, dyFromCenter: number, scale: number): Polar {
  return {
    phi: Math.hypot(dxFromCenter, dyFromCenter) / scale,
    theta: Math.atan2(-dyFromCenter, dxFromCenter),
  };
}

/** Output edge for PAN: a polar in-screen-plane slide (r, angle) → a world translation along the
 *  camera's right/up basis, scaled by worldPerPixel. The basis is taken from the camera quaternion
 *  here (the only Cartesian, quarantined like cameraFrame's), so the handler passes pure polar
 *  (r, angle): the mouse's screen plane is the equatorial plane of its own polar sphere (pole =
 *  view), and angle picks the in-plane direction the origin slides along. */
export function planeSlide(camQuat: THREE.Quaternion, r: number, angle: number, worldPerPixel: number): THREE.Vector3 {
  const right = new THREE.Vector3(1, 0, 0).applyQuaternion(camQuat);
  const up = new THREE.Vector3(0, 1, 0).applyQuaternion(camQuat);
  return right.multiplyScalar(r * Math.cos(angle) * worldPerPixel)
    .add(up.multiplyScalar(r * Math.sin(angle) * worldPerPixel));
}

// ---------------------------------------------------------------------------
// World-direction → Go-convention polar angles
// ---------------------------------------------------------------------------

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
