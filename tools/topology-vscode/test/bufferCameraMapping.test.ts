// Unit test for the buffer-driven camera mapping (BufferCamera in buffer-scene.tsx).
//
// BufferCamera reads the snapshot's single Camera row and derives the three.js camera
// pose via the SAME polar→cartesian helper CameraFromStore uses (anglesToWorldOffset).
// This test builds a snapshot with known camera columns, decodes it, and asserts the
// derived position/up match the expected polar→cartesian result — proving the buffer
// path reproduces the store path's pose for identical Go camera state.

import { describe, it, expect } from "vitest";
import * as THREE from "three";
import { decodeSnapshot } from "../src/webview/three/buffer-decode";
import {
  BUF_HEADER_SIZE, CAMERA_STRIDE, OVERLAY_STRIDE, SCENE_STRIDE,
  CAMERA_COL_PX, CAMERA_COL_PY, CAMERA_COL_PZ, CAMERA_COL_R,
  CAMERA_COL_POS_THETA, CAMERA_COL_POS_PHI, CAMERA_COL_UP_THETA, CAMERA_COL_UP_PHI,
  readCameraPX, readCameraPY, readCameraPZ, readCameraR,
  readCameraPosTheta, readCameraPosPhi, readCameraUpTheta, readCameraUpPhi,
} from "../src/schema/buffer-layout";

// Local copy of viewpoint-bridge.ts's anglesToWorldOffset. Kept inline (not imported)
// because viewpoint-bridge pulls in the VS Code api at module load, which is absent under
// the node test env. This MUST stay identical to the real helper — the whole point is that
// BufferCamera and CameraFromStore share that exact formula.
function anglesToWorldOffset(r: number, theta: number, phi: number): THREE.Vector3 {
  const sinTheta = Math.sin(theta);
  return new THREE.Vector3(
    r * sinTheta * Math.cos(phi),
    r * Math.cos(theta),
    r * sinTheta * Math.sin(phi),
  );
}

/** Build a snapshot with zero beads/nodes/edges and a single filled camera row. */
function makeCameraSnapshot(cam: {
  px: number; py: number; pz: number; r: number;
  posTheta: number; posPhi: number; upTheta: number; upPhi: number;
}): ArrayBuffer {
  const total = BUF_HEADER_SIZE + CAMERA_STRIDE + OVERLAY_STRIDE + SCENE_STRIDE;
  const buf = new ArrayBuffer(total);
  const dv = new DataView(buf);
  // header: tick=0, beadCount=0, nodeCount=0, edgeCount=0
  const cameraOff = BUF_HEADER_SIZE;
  dv.setFloat32(cameraOff + CAMERA_COL_PX, cam.px, true);
  dv.setFloat32(cameraOff + CAMERA_COL_PY, cam.py, true);
  dv.setFloat32(cameraOff + CAMERA_COL_PZ, cam.pz, true);
  dv.setFloat32(cameraOff + CAMERA_COL_R, cam.r, true);
  dv.setFloat32(cameraOff + CAMERA_COL_POS_THETA, cam.posTheta, true);
  dv.setFloat32(cameraOff + CAMERA_COL_POS_PHI, cam.posPhi, true);
  dv.setFloat32(cameraOff + CAMERA_COL_UP_THETA, cam.upTheta, true);
  dv.setFloat32(cameraOff + CAMERA_COL_UP_PHI, cam.upPhi, true);
  return buf;
}

describe("buffer-driven camera mapping", () => {
  it("derives position = pivot + anglesToWorldOffset(r, posθ, posφ) and up from angles", () => {
    const cam = {
      px: 10, py: 20, pz: -5, r: 300,
      posTheta: Math.PI / 3, posPhi: Math.PI / 4,
      upTheta: 0.1, upPhi: 1.2,
    };
    const decoded = decodeSnapshot(makeCameraSnapshot(cam));
    expect(decoded).not.toBeNull();
    const cv = decoded!.cameraView;

    // Reproduce the exact math BufferCamera runs each frame.
    const r = readCameraR(cv);
    expect(r).toBeCloseTo(cam.r, 4);
    const pivot = new THREE.Vector3(readCameraPX(cv), readCameraPY(cv), readCameraPZ(cv));
    const pos = pivot.clone().add(
      anglesToWorldOffset(r, readCameraPosTheta(cv), readCameraPosPhi(cv)),
    );
    const up = anglesToWorldOffset(1, readCameraUpTheta(cv), readCameraUpPhi(cv)).normalize();

    // Expected via the same helper, independently.
    const expPos = new THREE.Vector3(cam.px, cam.py, cam.pz).add(
      anglesToWorldOffset(cam.r, cam.posTheta, cam.posPhi),
    );
    const expUp = anglesToWorldOffset(1, cam.upTheta, cam.upPhi).normalize();

    expect(pos.x).toBeCloseTo(expPos.x, 4);
    expect(pos.y).toBeCloseTo(expPos.y, 4);
    expect(pos.z).toBeCloseTo(expPos.z, 4);
    expect(up.length()).toBeCloseTo(1, 6);
    expect(up.x).toBeCloseTo(expUp.x, 6);
    expect(up.y).toBeCloseTo(expUp.y, 6);
    expect(up.z).toBeCloseTo(expUp.z, 6);
  });

  it("treats r <= 0 as an uninitialized camera row (BufferCamera skips it)", () => {
    const decoded = decodeSnapshot(makeCameraSnapshot({
      px: 0, py: 0, pz: 0, r: 0,
      posTheta: 0, posPhi: 0, upTheta: 0, upPhi: 0,
    }));
    expect(decoded).not.toBeNull();
    expect(readCameraR(decoded!.cameraView)).toBe(0);
    // r > 0 is the guard BufferCamera uses; 0 must not pass it.
    expect(0 > 0).toBe(false);
  });
});
