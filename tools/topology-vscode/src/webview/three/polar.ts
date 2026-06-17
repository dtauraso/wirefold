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

/** The equatorial unit direction at azimuth θ (cos θ·refX + sin θ·refY). A pure angle → axis. */
export function equatorDir(f: PolarFrame, theta: number): THREE.Vector3 {
  return f.refX.clone().multiplyScalar(Math.cos(theta)).add(f.refY.clone().multiplyScalar(Math.sin(theta)));
}

/** Polar → world point on the sphere (edge conversion, for rendering only). */
export function toWorld(f: PolarFrame, q: Polar): THREE.Vector3 {
  const s = Math.sin(q.phi);
  const dir = f.pole.clone().multiplyScalar(Math.cos(q.phi)).add(equatorDir(f, q.theta).multiplyScalar(s));
  return f.center.clone().add(dir.multiplyScalar(f.radius));
}

/** Point along the equatorial direction at distance d from the center (an in-plane point, e.g.
 *  the foot of the trig triangle on the equator/green line). */
export function equatorPoint(f: PolarFrame, theta: number, d: number): THREE.Vector3 {
  return f.center.clone().add(equatorDir(f, theta).multiplyScalar(d));
}

/** World point on/near the sphere → polar (θ, φ) about the frame. Edge conversion for input
 *  (e.g. the cursor's raycast point), the mirror of toWorld. */
export function fromWorld(f: PolarFrame, worldPoint: THREE.Vector3): Polar {
  const d = worldPoint.clone().sub(f.center).normalize();
  const phi = Math.acos(THREE.MathUtils.clamp(d.dot(f.pole), -1, 1));
  const theta = Math.atan2(d.dot(f.refY), d.dot(f.refX));
  return { theta, phi };
}
