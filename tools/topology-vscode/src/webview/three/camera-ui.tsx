// camera-ui.tsx — standalone camera control UI widgets for ThreeView.
// DollyButtons, GlobalLabelsToggle, HomeButton — no scene/substrate logic.

import { useRef, useCallback } from "react";
import * as THREE from "three";
import type { RFNode, NodeData } from "../types";
import { sceneCenter, nodeWorldPos, nodeRadius } from "./geometry-helpers";
import { patchViewerState } from "../state/viewer-state";
import { scheduleViewSave } from "../save";

/** Write current camera position + quaternion to viewerState and schedule a save. */
function commitCamera(cam: THREE.PerspectiveCamera) {
  patchViewerState((v) => {
    v.camera3d = {
      position: [cam.position.x, cam.position.y, cam.position.z],
      quaternion: [cam.quaternion.x, cam.quaternion.y, cam.quaternion.z, cam.quaternion.w],
    };
  });
  scheduleViewSave();
}

// ---------------------------------------------------------------------------
// Widgets: Dolly buttons, Home button, Global labels toggle
// ---------------------------------------------------------------------------

/** DOLLY BUTTONS: hold ^/v to dolly in/out. Positive direction = toward scene (z decreases). */
export function DollyButtons({
  cameraRef,
  nodesRef,
}: {
  cameraRef: React.MutableRefObject<THREE.PerspectiveCamera | null>;
  nodesRef: React.MutableRefObject<RFNode<NodeData>[]>;
}) {
  const frameRef = useRef<ReturnType<typeof requestAnimationFrame> | null>(null);

  const startDolly = useCallback((sign: number) => {
    const tick = () => {
      const cam = cameraRef.current;
      if (!cam) return;
      // sign = +1 → dolly toward scene (move camera in -z of its view)
      const dir = new THREE.Vector3(0, 0, -sign).applyQuaternion(cam.quaternion);
      // Use distance to scene center for correct speed after rotation.
      const center = sceneCenter(nodesRef.current);
      const dist = cam.position.distanceTo(center);
      const speed = 0.008 * Math.max(dist, 10);
      cam.position.addScaledVector(dir, speed);
      frameRef.current = requestAnimationFrame(tick);
    };
    frameRef.current = requestAnimationFrame(tick);
  }, [cameraRef, nodesRef]);

  const stopDolly = useCallback(() => {
    if (frameRef.current !== null) {
      cancelAnimationFrame(frameRef.current);
      frameRef.current = null;
    }
    // Commit final camera position after the dolly gesture ends.
    if (cameraRef.current) commitCamera(cameraRef.current);
  }, [cameraRef]);

  const btnStyle: React.CSSProperties = {
    width: 32,
    height: 28,
    cursor: "pointer",
    background: "rgba(60,60,80,0.85)",
    border: "1px solid #555",
    borderRadius: 5,
    color: "#ddd",
    fontSize: 15,
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    userSelect: "none",
  };

  return (
    <div
      style={{
        position: "absolute",
        right: 12,
        bottom: 16,
        display: "flex",
        flexDirection: "column",
        gap: 4,
        pointerEvents: "auto",
        zIndex: 20,
      }}
    >
      {/* ^ = toward scene (dolly in) */}
      <div
        style={btnStyle}
        onMouseDown={() => startDolly(1)}
        onMouseUp={stopDolly}
        onMouseLeave={stopDolly}
        title="Dolly in"
      >▲</div>
      {/* v = away from scene (dolly out) */}
      <div
        style={btnStyle}
        onMouseDown={() => startDolly(-1)}
        onMouseUp={stopDolly}
        onMouseLeave={stopDolly}
        title="Dolly out"
      >▼</div>
    </div>
  );
}

/** HOME BUTTON: reframes the camera to fit all nodes in view. */
export function HomeButton({
  cameraRef,
  nodesRef,
  aspect,
}: {
  cameraRef: React.MutableRefObject<THREE.PerspectiveCamera | null>;
  nodesRef: React.MutableRefObject<RFNode<NodeData>[]>;
  aspect: number;
}) {
  const onClick = useCallback((e: React.MouseEvent) => {
    e.stopPropagation();
    const cam = cameraRef.current;
    const nodes = nodesRef.current;
    if (!cam || nodes.length === 0) return;

    let minX = Infinity, maxX = -Infinity, minY = Infinity, maxY = -Infinity, minZ = Infinity, maxZ = -Infinity;
    for (const n of nodes) {
      const p = nodeWorldPos(n);
      const r = nodeRadius(n);
      minX = Math.min(minX, p.x - r); maxX = Math.max(maxX, p.x + r);
      minY = Math.min(minY, p.y - r); maxY = Math.max(maxY, p.y + r);
      minZ = Math.min(minZ, p.z - r); maxZ = Math.max(maxZ, p.z + r);
    }

    const center = new THREE.Vector3((minX + maxX) / 2, (minY + maxY) / 2, (minZ + maxZ) / 2);
    const sizeX = maxX - minX;
    const sizeY = maxY - minY;
    const sizeZ = maxZ - minZ;
    const fovRad = (cam.fov * Math.PI) / 180;
    const dist = (Math.max(sizeX / aspect, sizeY) / 2) / Math.tan(fovRad / 2) + sizeZ / 2;
    const paddedDist = dist * 1.2;

    // Reset to a square-on view: place the camera straight in front of the
    // plane (along +z) and level its orientation. This clears any leftover
    // tilt/roll so panning slides the plane uniformly (no parallax swivel).
    const newPos = new THREE.Vector3(center.x, center.y, center.z + paddedDist);
    cam.position.copy(newPos);
    cam.up.set(0, 1, 0);
    cam.lookAt(center);
    commitCamera(cam);
  }, [cameraRef, nodesRef, aspect]);

  return (
    <div
      onClick={onClick}
      title="Fit diagram in view"
      style={{
        position: "absolute",
        top: 44,
        right: 12,
        background: "rgba(0,0,0,0.55)",
        borderRadius: 6,
        padding: "3px 7px",
        cursor: "pointer",
        pointerEvents: "auto",
        zIndex: 20,
        color: "#ddd",
        fontSize: 11,
        fontFamily: "monospace",
        userSelect: "none",
        display: "flex",
        alignItems: "center",
        gap: 4,
      }}
    >
      ⌂ fit
    </div>
  );
}

/** GLOBAL LABELS TOGGLE: top-right button to show/hide all labels. */
export function GlobalLabelsToggle({
  hidden,
  onClick,
}: {
  hidden: boolean;
  onClick: (e: React.MouseEvent) => void;
}) {
  return (
    <div
      onClick={onClick}
      title={hidden ? "Show labels" : "Hide labels"}
      style={{
        position: "absolute",
        top: 12,
        right: 12,
        background: "rgba(0,0,0,0.55)",
        borderRadius: 6,
        padding: "3px 7px",
        cursor: "pointer",
        pointerEvents: "auto",
        zIndex: 20,
        color: hidden ? "#888" : "#ddd",
        fontSize: 11,
        fontFamily: "monospace",
        userSelect: "none",
        display: "flex",
        alignItems: "center",
        gap: 4,
      }}
    >
      {hidden ? "▴" : "▾"} labels
    </div>
  );
}

