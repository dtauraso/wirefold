// camera-ui.tsx — standalone camera control UI widgets for ThreeView.
// GlobalLabelsToggle, BadgesToggle, HomeButton, RingsToggle, ScenePolesToggle, NodePolesToggle,
// AngleLabelsToggle — no scene/Go logic.

import React, { useCallback } from "react";
import * as THREE from "three";
import type { RFNode, NodeData } from "../types";
import { nodeWorldPos, nodeRadius } from "./geometry-helpers";
import { patchViewerState } from "../state/viewer-state";
import { scheduleViewSave } from "../save";
import { vscode } from "../vscode-api";
import { useCameraStore } from "./camera-store";
import { postLog } from "../log/post";

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
// Widgets: Home button, Global labels toggle
// ---------------------------------------------------------------------------

/** HOME BUTTON: reframes the camera to fit all nodes in view. */
export function HomeButton({
  cameraRef,
  nodesRef,
  targetRef,
  aspect,
}: {
  cameraRef: React.MutableRefObject<THREE.PerspectiveCamera | null>;
  nodesRef: React.MutableRefObject<RFNode<NodeData>[]>;
  targetRef: React.MutableRefObject<THREE.Vector3>;
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
    // Seed the persistent pivot to the framed scene center so subsequent
    // orbit/pan/dolly operate around what Fit just framed to.
    targetRef.current.copy(center);
    commitCamera(cam);
  }, [cameraRef, nodesRef, targetRef, aspect]);

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

/** RINGS TOGGLE: top-right button to show/hide the polar-guide tori. Fire-and-forget to Go. */
/** GUIDELINES MASTER: TS-only toggle that activates/deactivates the whole polar-guideline
 * group (rings/tori, scene poles, node poles, angle labels, sel-sphere poles). When inactive
 * it hides those 5 buttons (gated in ThreeView) and NavGuides suppresses every guide; each
 * guide's own Go-owned state is left untouched, so reactivating restores the prior states. */
export function GuidelinesToggle() {
  const active = useCameraStore((s) => s.guidelinesActive);
  const setActive = useCameraStore((s) => s.setGuidelinesActive);
  const onClick = useCallback((e: React.MouseEvent) => {
    e.stopPropagation();
    const next = active === false;
    postLog("guide-btn-click", { op: "guidelines", wasActive: active !== false, nextActive: next });
    setActive(next);
    patchViewerState((v) => { v.guidelinesActive = next ? undefined : false; });
    scheduleViewSave();
  }, [active, setActive]);
  return (
    <div
      onClick={onClick}
      title={active !== false ? "Hide polar guidelines" : "Show polar guidelines"}
      style={{
        position: "absolute",
        top: 76,
        right: 12,
        background: "rgba(0,0,0,0.55)",
        borderRadius: 6,
        padding: "3px 7px",
        cursor: "pointer",
        pointerEvents: "auto",
        zIndex: 20,
        color: active !== false ? "#ddd" : "#888",
        fontSize: 11,
        fontFamily: "monospace",
        userSelect: "none",
        display: "flex",
        alignItems: "center",
        gap: 4,
      }}
    >
      ▦ guidelines
    </div>
  );
}

export function RingsToggle() {
  const visible = useCameraStore((s) => s.sceneToriVisible);
  const onClick = useCallback((e: React.MouseEvent) => {
    e.stopPropagation();
    // Fire-and-forget: Go owns the toggle state and echoes back via scene-tori.
    postLog("guide-btn-click", { op: "tori-vis", was: visible });
    vscode.postMessage({ type: "edit", op: "tori-vis" });
  }, [visible]);
  return (
    <div
      onClick={onClick}
      title={visible ? "Hide polar rings" : "Show polar rings"}
      style={{
        position: "absolute",
        top: 104,
        right: 12,
        background: "rgba(0,0,0,0.55)",
        borderRadius: 6,
        padding: "3px 7px",
        cursor: "pointer",
        pointerEvents: "auto",
        zIndex: 20,
        color: visible ? "#ddd" : "#888",
        fontSize: 11,
        fontFamily: "monospace",
        userSelect: "none",
        display: "flex",
        alignItems: "center",
        gap: 4,
      }}
    >
      ◎ rings
    </div>
  );
}

/** SCENE POLES TOGGLE: top-right button to show/hide the scene-center pole frame. Fire-and-forget to Go. */
export function ScenePolesToggle() {
  const visible = useCameraStore((s) => s.scenePolesVisible);
  const onClick = useCallback((e: React.MouseEvent) => {
    e.stopPropagation();
    // Fire-and-forget: Go owns the toggle state and echoes back via scene-poles.
    postLog("guide-btn-click", { op: "scene-poles", was: visible });
    vscode.postMessage({ type: "edit", op: "scene-poles" });
  }, [visible]);
  return (
    <div
      onClick={onClick}
      title={visible ? "Hide scene pole frame" : "Show scene pole frame"}
      style={{
        position: "absolute",
        top: 132,
        right: 12,
        background: "rgba(0,0,0,0.55)",
        borderRadius: 6,
        padding: "3px 7px",
        cursor: "pointer",
        pointerEvents: "auto",
        zIndex: 20,
        color: visible ? "#ddd" : "#888",
        fontSize: 11,
        fontFamily: "monospace",
        userSelect: "none",
        display: "flex",
        alignItems: "center",
        gap: 4,
      }}
    >
      ⊹ scene poles
    </div>
  );
}

/** NODE POLES TOGGLE: top-right button to show/hide per-node pole frames. Fire-and-forget to Go. */
export function NodePolesToggle() {
  const visible = useCameraStore((s) => s.nodePolesVisible);
  const onClick = useCallback((e: React.MouseEvent) => {
    e.stopPropagation();
    // Fire-and-forget: Go owns the toggle state and echoes back via node-poles.
    postLog("guide-btn-click", { op: "node-poles", was: visible });
    vscode.postMessage({ type: "edit", op: "node-poles" });
  }, [visible]);
  return (
    <div
      onClick={onClick}
      title={visible ? "Hide node pole frames" : "Show node pole frames"}
      style={{
        position: "absolute",
        top: 160,
        right: 12,
        background: "rgba(0,0,0,0.55)",
        borderRadius: 6,
        padding: "3px 7px",
        cursor: "pointer",
        pointerEvents: "auto",
        zIndex: 20,
        color: visible ? "#ddd" : "#888",
        fontSize: 11,
        fontFamily: "monospace",
        userSelect: "none",
        display: "flex",
        alignItems: "center",
        gap: 4,
      }}
    >
      ⊹ node poles
    </div>
  );
}

/** ANGLE LABELS TOGGLE: top-right button to show/hide θ/φ angle arcs+labels. Fire-and-forget to Go. */
export function AngleLabelsToggle() {
  const visible = useCameraStore((s) => s.angleLabelsVisible);
  const onClick = useCallback((e: React.MouseEvent) => {
    e.stopPropagation();
    // Fire-and-forget: Go owns the toggle state and echoes back via angle-labels.
    postLog("guide-btn-click", { op: "angle-labels", was: visible });
    vscode.postMessage({ type: "edit", op: "angle-labels" });
  }, [visible]);
  return (
    <div
      onClick={onClick}
      title={visible ? "Hide angle arcs+labels" : "Show angle arcs+labels"}
      style={{
        position: "absolute",
        top: 188,
        right: 12,
        background: "rgba(0,0,0,0.55)",
        borderRadius: 6,
        padding: "3px 7px",
        cursor: "pointer",
        pointerEvents: "auto",
        zIndex: 20,
        color: visible ? "#ddd" : "#888",
        fontSize: 11,
        fontFamily: "monospace",
        userSelect: "none",
        display: "flex",
        alignItems: "center",
        gap: 4,
      }}
    >
      θφ 2→3/7
    </div>
  );
}

/** SEL SPHERE POLES TOGGLE: top-right button to show/hide selection-sphere pole axis markers. Fire-and-forget to Go. */
export function SelSpherePolesToggle() {
  const visible = useCameraStore((s) => s.selSpherePolesVisible);
  const onClick = useCallback((e: React.MouseEvent) => {
    e.stopPropagation();
    // Fire-and-forget: Go owns the toggle state and echoes back via sel-sphere-poles.
    postLog("guide-btn-click", { op: "sel-sphere-poles", was: visible });
    vscode.postMessage({ type: "edit", op: "sel-sphere-poles" });
  }, [visible]);
  return (
    <div
      onClick={onClick}
      title={visible ? "Hide sel-sphere poles" : "Show sel-sphere poles"}
      style={{
        position: "absolute",
        top: 216,
        right: 12,
        background: "rgba(0,0,0,0.55)",
        borderRadius: 6,
        padding: "3px 7px",
        cursor: "pointer",
        pointerEvents: "auto",
        zIndex: 20,
        color: visible ? "#ddd" : "#888",
        fontSize: 11,
        fontFamily: "monospace",
        userSelect: "none",
        display: "flex",
        alignItems: "center",
        gap: 4,
      }}
    >
      sel ⬡
    </div>
  );
}

/** HANDHOLDS TOGGLE: top-right button to show/hide the rotation grab spheres. Standalone — NOT gated by guidelinesActive. Fire-and-forget to Go. */
export function HandholdsToggle() {
  const visible = useCameraStore((s) => s.handholdsVisible);
  const onClick = useCallback((e: React.MouseEvent) => {
    e.stopPropagation();
    // Fire-and-forget: Go owns the toggle state and echoes back via handholds.
    postLog("guide-btn-click", { op: "handholds-vis", was: visible });
    vscode.postMessage({ type: "edit", op: "handholds-vis" });
  }, [visible]);
  return (
    <div
      onClick={onClick}
      title={visible !== false ? "Hide rotation grips" : "Show rotation grips"}
      style={{
        position: "absolute",
        top: 248,
        right: 12,
        background: "rgba(0,0,0,0.55)",
        borderRadius: 6,
        padding: "3px 7px",
        cursor: "pointer",
        pointerEvents: "auto",
        zIndex: 20,
        color: visible !== false ? "#ddd" : "#888",
        fontSize: 11,
        fontFamily: "monospace",
        userSelect: "none",
        display: "flex",
        alignItems: "center",
        gap: 4,
      }}
    >
      ⊙ grips
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

/** BADGES TOGGLE: top-right button to show/hide occlusion +N badges. */
export function BadgesToggle({
  hidden,
  onClick,
}: {
  hidden: boolean;
  onClick: (e: React.MouseEvent) => void;
}) {
  return (
    <div
      onClick={onClick}
      title={hidden ? "Show +N badges" : "Hide +N badges"}
      style={{
        position: "absolute",
        top: 280,
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
      {hidden ? "▴" : "▾"} +N badges
    </div>
  );
}

