// BufferCamera.tsx — buffer-driven camera: each frame reads the snapshot's single Camera row
// and drives the three.js camera (position / up / lookAt) from it. Split out of
// buffer-scene.tsx: pure buffer→camera render, no state authority.
//
// The polar→cartesian mapping is IDENTICAL to CameraFromStore's (anglesToWorldOffset in
// viewpoint-bridge), so a given Go camera state produces the same three.js pose on
// either path:
//   pivot   = (PX, PY, PZ)
//   position = pivot + anglesToWorldOffset(R, PosTheta, PosPhi)
//   up      = anglesToWorldOffset(1, UpTheta, UpPhi).normalize()
//   lookAt(pivot)
//
// Also keeps `cameraRef` current (the old CameraRefBridge did this; it is gated off
// under the flag, but raw-input / HomeButton still read cameraRef.current).

import { useRef } from "react";
import { useFrame, useThree } from "@react-three/fiber";
import * as THREE from "three";
import { getViewBlocks } from "./view-blocks";
import { anglesToWorldOffset } from "./viewpoint-bridge";
import {
  readCameraPX, readCameraPY, readCameraPZ, readCameraR,
  readCameraPosTheta, readCameraPosPhi, readCameraUpTheta, readCameraUpPhi,
} from "../../schema/buffer-layout";

export function BufferCamera({ cameraRef }: {
  cameraRef?: React.MutableRefObject<THREE.PerspectiveCamera | null>;
}) {
  const { camera } = useThree();
  const pivotRef = useRef(new THREE.Vector3());

  useFrame(() => {
    const cam = camera as THREE.PerspectiveCamera;
    if (cameraRef) cameraRef.current = cam; // keep the ref alive for raw-input / HomeButton

    const blocks = getViewBlocks();
    if (!blocks) return;
    const cv = blocks.cameraView;

    const r = readCameraR(cv);
    // Guard the uninitialized camera row: Go emits a real viewpoint on load (SeedInitialViewpoint
    // reads the saved pose from view/scene.json, or a non-degenerate default), but node-geometry
    // snapshots can land first, with the camera row still all zeros. r <= 0 means "no viewpoint
    // yet" — skip, mirroring CameraFromStore's `!polar`.
    if (!(r > 0)) return;

    const pivot = pivotRef.current;
    pivot.set(readCameraPX(cv), readCameraPY(cv), readCameraPZ(cv));
    const posOffset = anglesToWorldOffset(r, readCameraPosTheta(cv), readCameraPosPhi(cv));
    cam.position.copy(pivot).add(posOffset);
    const upDir = anglesToWorldOffset(1, readCameraUpTheta(cv), readCameraUpPhi(cv)).normalize();
    cam.up.copy(upDir);
    cam.lookAt(pivot);
    cam.updateMatrixWorld(true);
  });

  return null;
}
