// Phase 2 port-drag math: pointerRingAnchor projects a world-space pointer hit onto
// a node's ring plane, returning the node-center-relative direction constrained to
// the ring (z=0). This is interaction input (the anchor sent to Go), not wire/bead
// geometry — Go normalizes the result, so only direction (not magnitude) matters.

import { describe, it, expect } from "vitest";
import * as THREE from "three";
import { pointerRingAnchor } from "../src/webview/three/geometry-helpers";

const center = new THREE.Vector3(10, 20, 0);

describe("pointerRingAnchor", () => {
  it("returns the in-plane offset from center to hit, with z zeroed", () => {
    const hit = new THREE.Vector3(15, 20, 0); // 5 to the right of center
    const a = pointerRingAnchor(center, hit);
    expect(a).toEqual({ x: 5, y: 0, z: 0 });
  });

  it("zeroes any z component of the hit (constrains to the ring)", () => {
    const hit = new THREE.Vector3(10, 25, 7); // above center, off-plane in z
    const a = pointerRingAnchor(center, hit);
    expect(a).toEqual({ x: 0, y: 5, z: 0 });
  });

  it("points the right direction regardless of magnitude (Go normalizes)", () => {
    const near = pointerRingAnchor(center, new THREE.Vector3(11, 21, 0));
    const far = pointerRingAnchor(center, new THREE.Vector3(40, 50, 0));
    expect(near).not.toBeNull();
    expect(far).not.toBeNull();
    const nu = new THREE.Vector3(near!.x, near!.y, near!.z).normalize();
    const fu = new THREE.Vector3(far!.x, far!.y, far!.z).normalize();
    expect(nu.distanceTo(fu)).toBeLessThan(1e-9);
  });

  it("returns null when the hit is exactly at the node center", () => {
    expect(pointerRingAnchor(center, new THREE.Vector3(10, 20, 0))).toBeNull();
    // z-only difference still collapses to center in-plane → null
    expect(pointerRingAnchor(center, new THREE.Vector3(10, 20, 9))).toBeNull();
  });
});
