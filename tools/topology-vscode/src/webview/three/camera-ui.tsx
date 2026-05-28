// camera-ui.tsx — standalone camera control UI widgets for ThreeView.
// RollSlider, DollyButtons, PanPad — no scene/substrate logic.

import { useRef, useState, useCallback } from "react";
import * as THREE from "three";
import type { RFNode, NodeData } from "../types";
import { sceneCenter, worldPerPixel, nodeWorldPos, nodeRadius } from "./geometry-helpers";
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
// Widgets: Roll slider, Dolly buttons, Pan pad
// ---------------------------------------------------------------------------

/** ROLL SLIDER: vertical slider (range -π..π) that rolls camera about its view axis. */
export function RollSlider({ cameraRef }: { cameraRef: React.MutableRefObject<THREE.PerspectiveCamera | null> }) {
  const [rollDeg, setRollDeg] = useState(0);
  const prevRoll = useRef(0);

  const onChange = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
    const cam = cameraRef.current;
    if (!cam) return;
    const newDeg = parseFloat(e.target.value);
    const delta = newDeg - prevRoll.current;
    prevRoll.current = newDeg;
    setRollDeg(newDeg);

    // Roll camera about its forward (z) axis (local -z = forward; roll about it).
    const forward = new THREE.Vector3(0, 0, -1).applyQuaternion(cam.quaternion);
    const q = new THREE.Quaternion().setFromAxisAngle(forward, (delta * Math.PI) / 180);
    cam.quaternion.premultiply(q);
    // Commit on each step; scheduleViewSave debounces the actual write.
    commitCamera(cam);
  }, [cameraRef]);

  return (
    <div
      style={{
        position: "absolute",
        right: 12,
        top: "50%",
        transform: "translateY(-50%)",
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        gap: 4,
        background: "rgba(0,0,0,0.55)",
        borderRadius: 8,
        padding: "8px 6px",
        pointerEvents: "auto",
        zIndex: 20,
        userSelect: "none",
      }}
    >
      <span style={{ color: "#aaa", fontSize: 9, fontFamily: "monospace" }}>ROLL</span>
      <input
        type="range"
        min={-180}
        max={180}
        step={1}
        value={rollDeg}
        onChange={onChange}
        style={{
          writingMode: "vertical-lr",
          direction: "rtl",
          width: 20,
          height: 120,
          cursor: "pointer",
          accentColor: "#4af",
        }}
      />
      <span style={{ color: "#aaa", fontSize: 9, fontFamily: "monospace" }}>{rollDeg}°</span>
    </div>
  );
}

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

    const forward = new THREE.Vector3(0, 0, -1).applyQuaternion(cam.quaternion);
    const newPos = center.clone().addScaledVector(forward, -paddedDist);
    cam.position.copy(newPos);
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

/** PAN PAD: small floating pad that appears on dwell; drag to pan the camera. */
export function PanPad({
  origin,
  cameraRef,
  canvasSize,
}: {
  origin: { x: number; y: number };
  cameraRef: React.MutableRefObject<THREE.PerspectiveCamera | null>;
  canvasSize: { w: number; h: number };
}) {
  const startPos = useRef({ x: 0, y: 0 });
  const camStartPos = useRef(new THREE.Vector3());
  const dragging = useRef(false);

  const onPadPointerDown = useCallback((e: React.PointerEvent) => {
    e.stopPropagation();
    dragging.current = true;
    startPos.current = { x: e.clientX, y: e.clientY };
    camStartPos.current = cameraRef.current?.position.clone() ?? new THREE.Vector3();
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
  }, [cameraRef]);

  const onPadPointerMove = useCallback((e: React.PointerEvent) => {
    if (!dragging.current) return;
    e.stopPropagation();
    const cam = cameraRef.current;
    if (!cam) return;
    const dx = e.clientX - startPos.current.x;
    const dy = e.clientY - startPos.current.y;
    // worldPerPixel uses true perpendicular distance — correct after rotation.
    const wpp = worldPerPixel(cam, canvasSize.h);
    const rightDir = new THREE.Vector3().setFromMatrixColumn(cam.matrixWorld, 0);
    const upDir = new THREE.Vector3().setFromMatrixColumn(cam.matrixWorld, 1);
    cam.position.copy(camStartPos.current)
      .addScaledVector(rightDir, -dx * wpp)
      .addScaledVector(upDir, dy * wpp);
  }, [cameraRef, canvasSize]);

  const onPadPointerUp = useCallback((e: React.PointerEvent) => {
    e.stopPropagation();
    dragging.current = false;
    // Commit final camera position after the pan gesture ends.
    if (cameraRef.current) commitCamera(cameraRef.current);
  }, [cameraRef]);

  const PAD_SIZE = 64;
  return (
    <div
      onPointerDown={onPadPointerDown}
      onPointerMove={onPadPointerMove}
      onPointerUp={onPadPointerUp}
      style={{
        position: "absolute",
        left: origin.x - PAD_SIZE / 2,
        top: origin.y - PAD_SIZE / 2,
        width: PAD_SIZE,
        height: PAD_SIZE,
        background: "rgba(80,120,200,0.35)",
        border: "1.5px solid rgba(100,160,255,0.7)",
        borderRadius: "50%",
        cursor: "grab",
        pointerEvents: "auto",
        zIndex: 30,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        color: "rgba(200,220,255,0.8)",
        fontSize: 10,
        fontFamily: "monospace",
        userSelect: "none",
      }}
    >
      PAN
    </div>
  );
}
