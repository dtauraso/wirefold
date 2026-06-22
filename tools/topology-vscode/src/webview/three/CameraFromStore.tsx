// CameraFromStore.tsx — output edge: applies Go's polar camera state to the three.js camera.
// Reads useCameraStore; on each change, repositions and orients the live camera.
// Must be mounted inside <Canvas> so useThree() is available.

import { useEffect } from "react";
import { useThree } from "@react-three/fiber";
import * as THREE from "three";
import { useCameraStore } from "./camera-store";
import { anglesToWorldOffset } from "./viewpoint-bridge";

export function CameraFromStore() {
  const { camera } = useThree();
  const polar = useCameraStore((s) => s.camera);

  useEffect(() => {
    if (!polar) return;
    const cam = camera as THREE.PerspectiveCamera;
    const pivotVec = new THREE.Vector3(...polar.pivot);
    const posOffset = anglesToWorldOffset(polar.r, polar.pos[0], polar.pos[1]);
    cam.position.copy(pivotVec.clone().add(posOffset));
    const upDir = anglesToWorldOffset(1, polar.up[0], polar.up[1]).normalize();
    cam.up.copy(upDir);
    cam.lookAt(pivotVec);
    cam.updateMatrixWorld(true);
  }, [polar, camera]);

  return null;
}
