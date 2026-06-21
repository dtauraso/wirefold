// polar.ts — polar/spherical primitives for navigation geometry.
//
// The model is POLAR: a direction on the sphere is an angle pair, not an (x,y,z) vector, and
// operations are angle arithmetic. Cartesian is quarantined to two edge helpers — makeFrame
// (builds the reference axes once) and toWorld (converts an angle pair to a world point for
// rendering). Navigation logic imports only this file, so there is no cross product or world
// vector in reach to "sneak in" — and no cross-product degeneracy (θ+90° is always defined).

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

/** Build the frame from a center, radius and pole direction. The single contained cross
 *  product picks equatorial axes; it can't degenerate because the seed is chosen non-parallel
 *  to the pole. Logic above never sees it. */
export function makeFrame(center: THREE.Vector3, radius: number, pole: THREE.Vector3): PolarFrame {
  const p = pole.clone().normalize();
  const seed = Math.abs(p.y) < 0.9 ? new THREE.Vector3(0, 1, 0) : new THREE.Vector3(1, 0, 0);
  const refX = new THREE.Vector3().crossVectors(seed, p).normalize();
  const refY = new THREE.Vector3().crossVectors(p, refX).normalize();
  return { center, radius, pole: p, refX, refY };
}

/** The equatorial unit direction at azimuth θ (cos θ·refX + sin θ·refY). A pure angle → axis.
 *  Internal to the toolkit (used by toWorld / arcAxisAngle). */
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

const _ORIGIN = new THREE.Vector3(0, 0, 0);

/** The rotation carrying unit direction `from` to unit direction `to`, as an AXIS + arc ANGLE,
 *  with the axis from θ+90° — NO cross of the two points. Build a frame poled at `from`; `to`
 *  sits at azimuth θc and arc φc. The great-circle axis is the equatorial direction at θc+90°
 *  (perpendicular to the from→to arc — pure angle arithmetic), and the rotation angle is φc.
 *  The only Cartesian is the makeFrame/fromWorld edges that define the frame.
 *  Sign: θc+π/2 (not θc-π/2) — verified by Rodrigues: axis×from = equatorDir(θc) so
 *  rotateAboutAxis(from,axis,φc) = from·cos(φc)+equatorDir(θc)·sin(φc) = to. ✓ */
export function arcAxisAngle(from: THREE.Vector3, to: THREE.Vector3): { axis: THREE.Vector3; angle: number } {
  const f = makeFrame(_ORIGIN.clone(), 1, from);
  const p = fromWorld(f, to);                          // (θc, φc) of `to` about `from`
  const axis = equatorDir(f, p.theta + Math.PI / 2);   // θ+90°: axis ⟂ the from→to arc
  return { axis, angle: p.phi };
}

/** The signed rotation about a FIXED `axis` that carries unit direction `from` to `to`,
 *  measured as the azimuth difference in a frame poled at the axis (θ_to − θ_from), wrapped
 *  to (−π, π]. This is the locked-disk quantity: the disk normal is `axis`, and the result is
 *  how far to spin about it so `from`'s in-plane bearing reaches `to`'s — pure angle arithmetic
 *  (the only Cartesian is the makeFrame/fromWorld edges). Used by handhold-constrained rotation
 *  where the axis is frozen at gesture start and only the angle tracks the cursor. */
export function angleAboutAxis(from: THREE.Vector3, to: THREE.Vector3, axis: THREE.Vector3): number {
  const f = makeFrame(_ORIGIN.clone(), 1, axis);
  const a = fromWorld(f, from);
  const b = fromWorld(f, to);
  let d = b.theta - a.theta;
  if (d > Math.PI) d -= 2 * Math.PI;
  if (d < -Math.PI) d += 2 * Math.PI;
  return d;
}

/** Rotate a unit direction `v` about a unit `axis` by `angle` radians — the rotation IS
 *  `θ += angle` in a frame poled at the axis. No quaternion, no Rodrigues. A point on the axis
 *  (φ≈0) is unmoved because the θ term is scaled by sin(φ)→0 — harmless. */
export function rotateAboutAxis(v: THREE.Vector3, axis: THREE.Vector3, angle: number): THREE.Vector3 {
  const f = makeFrame(_ORIGIN.clone(), 1, axis);
  const p = fromWorld(f, v);
  return toWorld(f, { theta: p.theta + angle, phi: p.phi });
}

/** Radial edge of the polar toolkit: move `point` to a new polar radius `r` about `center`,
 *  holding its angular direction. `r` (current) and `rNew` (floored at `minDist`) are explicit
 *  named coordinates — this is the zoom/dolly operation as pure radius. Direction is preserved
 *  WITHOUT decomposing to (θ, φ), so it has no pole singularity (a fromWorld→toWorld round-trip
 *  WOULD add one, since θ is undefined along the pole). The contained Cartesian is quarantined
 *  here like makeFrame/toWorld. Returns the new world position. */
export function scaleRadius(center: THREE.Vector3, point: THREE.Vector3, factor: number, minDist: number): THREE.Vector3 {
  const r = point.distanceTo(center); // explicit polar radius
  if (!Number.isFinite(r) || r < 1e-9 || !Number.isFinite(factor)) return point.clone();
  const rNew = Math.max(r * factor, minDist); // explicit new radius, floored
  return center.clone().add(point.clone().sub(center).multiplyScalar(rNew / r));
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
