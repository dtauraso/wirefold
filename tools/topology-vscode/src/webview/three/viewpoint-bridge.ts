// viewpoint-bridge.ts — polar frame-convention helpers (Go uses pole = +y).
//
// The camera is Go-owned: the gesture FSM (nodes/Wiring/gesture.go) applies every orbit /
// zoom / pan / set from raw-input IN-PROCESS and streams the resulting pose out in the
// content buffer. There is NO camera edit on the TS→Go wire — the old sendViewpoint* senders
// (encodeEditUpdate "camera") were removed with the JSON leaf. What remains here is the
// pure polar↔world math BufferCamera uses to place the three.js camera from the buffer's
// Camera row (anglesToWorldOffset).

import * as THREE from "three";

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
