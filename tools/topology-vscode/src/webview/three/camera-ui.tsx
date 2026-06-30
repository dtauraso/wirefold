// camera-ui.tsx — standalone camera control UI widgets for ThreeView.
// GlobalLabelsToggle, BadgesToggle, HomeButton, RingsToggle, ScenePolesToggle, NodePolesToggle,
// AngleLabelsToggle — no scene/Go logic.

import React, { useCallback } from "react";
import * as THREE from "three";
import type { RFNode, NodeData } from "../types";
import { nodeWorldPos, nodeRadius } from "./geometry-helpers";
import { vscode } from "../vscode-api";
import { useCameraStore } from "./camera-store";
import { postLog } from "../log/post";
import { commitCamera } from "./interaction-handlers";

// ---------------------------------------------------------------------------
// Shared Toggle component
// ---------------------------------------------------------------------------

/** The bare-payload toggle `op`s — `edit` messages that carry only `{ type, op }`. */
type EditOp =
  | "overlays-vis"
  | "tori-vis"
  | "scene-poles"
  | "node-poles"
  | "angle-labels"
  | "sel-sphere-poles"
  | "handholds-vis"
  | "labels-vis"
  | "badges-vis";

type ToggleCfg = {
  top: number;
  op: EditOp;
  /** Returns the store boolean value needed by active/label/title. */
  selector: (s: Parameters<Parameters<typeof useCameraStore>[0]>[0]) => boolean;
  /** Compute active (highlight) from the raw store value. */
  active: (val: boolean) => boolean;
  /** Label string or function of raw store value. */
  label: string | ((val: boolean) => string);
  /** Title string function of active value. */
  title: (active: boolean) => string;
  /** postLog payload factory. */
  payload: (val: boolean) => Record<string, unknown>;
};

function Toggle({ cfg }: { cfg: ToggleCfg }) {
  const val = useCameraStore(cfg.selector);
  const active = cfg.active(val);
  const onClick = useCallback(
    (e: React.MouseEvent) => {
      e.stopPropagation();
      postLog("guide-btn-click", cfg.payload(val));
      // op is one of the bare toggle ops, each a { type:"edit", op } variant of
      // WebviewToHostMsg; the union->member assertion mirrors the original literals.
      vscode.postMessage({ type: "edit", op: cfg.op } as Parameters<typeof vscode.postMessage>[0]);
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [val]
  );
  return (
    <div
      onClick={onClick}
      title={cfg.title(active)}
      style={{
        position: "absolute",
        top: cfg.top,
        right: 12,
        background: "rgba(0,0,0,0.55)",
        borderRadius: 6,
        padding: "3px 7px",
        cursor: "pointer",
        pointerEvents: "auto",
        zIndex: 20,
        color: active ? "#ddd" : "#888",
        fontSize: 11,
        fontFamily: "monospace",
        userSelect: "none",
        display: "flex",
        alignItems: "center",
        gap: 4,
      }}
    >
      {typeof cfg.label === "function" ? cfg.label(val) : cfg.label}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Config table for the 9 toggle buttons
// ---------------------------------------------------------------------------

const guidelinesCfg: ToggleCfg = {
  top: 76,
  op: "overlays-vis",
  selector: (s) => s.overlaysVisible,
  active: (v) => v,
  label: "▦ overlays",
  title: (a) => (a ? "Hide overlays" : "Show overlays"),
  payload: (v) => ({ op: "overlays-vis", was: v }),
};

const ringsCfg: ToggleCfg = {
  top: 104,
  op: "tori-vis",
  selector: (s) => s.sceneToriVisible,
  active: (v) => v,
  label: "◎ rings",
  title: (a) => (a ? "Hide polar rings" : "Show polar rings"),
  payload: (v) => ({ op: "tori-vis", was: v }),
};

const scenePolesCfg: ToggleCfg = {
  top: 132,
  op: "scene-poles",
  selector: (s) => s.scenePolesVisible,
  active: (v) => v,
  label: "⊹ scene poles",
  title: (a) => (a ? "Hide scene pole frame" : "Show scene pole frame"),
  payload: (v) => ({ op: "scene-poles", was: v }),
};

const nodePolesCfg: ToggleCfg = {
  top: 160,
  op: "node-poles",
  selector: (s) => s.nodePolesVisible,
  active: (v) => v,
  label: "⊹ node poles",
  title: (a) => (a ? "Hide node pole frames" : "Show node pole frames"),
  payload: (v) => ({ op: "node-poles", was: v }),
};

const angleLabelsCfg: ToggleCfg = {
  top: 188,
  op: "angle-labels",
  selector: (s) => s.angleLabelsVisible,
  active: (v) => v,
  label: "θφ 2→3/7",
  title: (a) => (a ? "Hide angle arcs+labels" : "Show angle arcs+labels"),
  payload: (v) => ({ op: "angle-labels", was: v }),
};

const selSpherePolesCfg: ToggleCfg = {
  top: 216,
  op: "sel-sphere-poles",
  selector: (s) => s.selSpherePolesVisible,
  active: (v) => v,
  label: "sel ⬡",
  title: (a) => (a ? "Hide sel-sphere poles" : "Show sel-sphere poles"),
  payload: (v) => ({ op: "sel-sphere-poles", was: v }),
};

const handholdsCfg: ToggleCfg = {
  top: 248,
  op: "handholds-vis",
  selector: (s) => s.handholdsVisible,
  active: (v) => v !== false,
  label: "⊙ grips",
  title: (a) => (a ? "Hide rotation grips" : "Show rotation grips"),
  payload: (v) => ({ op: "handholds-vis", was: v }),
};

const globalLabelsCfg: ToggleCfg = {
  top: 12,
  op: "labels-vis",
  selector: (s) => s.labelsGlobalHidden,
  active: (v) => !v,
  label: (v) => `${v ? "▴" : "▾"} labels`,
  title: (a) => (a ? "Hide labels" : "Show labels"),
  payload: (v) => ({ op: "labels-vis", wasHidden: v }),
};

const badgesCfg: ToggleCfg = {
  top: 280,
  op: "badges-vis",
  selector: (s) => s.badgesHidden,
  active: (v) => !v,
  label: (v) => `${v ? "▴" : "▾"} +N badges`,
  title: (a) => (a ? "Hide +N badges" : "Show +N badges"),
  payload: (v) => ({ op: "badges-vis", wasHidden: v }),
};

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

/** OVERLAYS MASTER: Go-owned toggle that shows/hides all 8 overlays at once.
 * When hidden it hides all 8 sub-buttons (gated in ThreeView) and NavGuides suppresses
 * every overlay; each overlay's own Go-owned state is left untouched, so reactivating
 * restores the prior per-overlay states. Fire-and-forget to Go; Go echoes back via
 * overlays-vis trace event. */
export function GuidelinesToggle() { return <Toggle cfg={guidelinesCfg} />; }

export function RingsToggle() { return <Toggle cfg={ringsCfg} />; }

/** SCENE POLES TOGGLE: top-right button to show/hide the scene-center pole frame. Fire-and-forget to Go. */
export function ScenePolesToggle() { return <Toggle cfg={scenePolesCfg} />; }

/** NODE POLES TOGGLE: top-right button to show/hide per-node pole frames. Fire-and-forget to Go. */
export function NodePolesToggle() { return <Toggle cfg={nodePolesCfg} />; }

/** ANGLE LABELS TOGGLE: top-right button to show/hide θ/φ angle arcs+labels. Fire-and-forget to Go. */
export function AngleLabelsToggle() { return <Toggle cfg={angleLabelsCfg} />; }

/** SEL SPHERE POLES TOGGLE: top-right button to show/hide selection-sphere pole axis markers. Fire-and-forget to Go. */
export function SelSpherePolesToggle() { return <Toggle cfg={selSpherePolesCfg} />; }

/** HANDHOLDS TOGGLE: top-right button to show/hide the rotation grab spheres. Standalone — NOT gated by guidelinesActive. Fire-and-forget to Go. */
export function HandholdsToggle() { return <Toggle cfg={handholdsCfg} />; }

/** GLOBAL LABELS TOGGLE: top-right button to show/hide all labels.
 *  Reads labelsGlobalHidden from camera-store (Go-owned via labels-global trace events).
 *  Click dispatches fire-and-forget "labels-vis" to Go; Go echoes back the new state.
 */
export function GlobalLabelsToggle() { return <Toggle cfg={globalLabelsCfg} />; }

/** DOUBLE-LINKS TOGGLE: Go-owned toggle for the bidirectional edge overlay.
 * When active, edge tubes are dimmed and each edge shows a cyan bidirectional arrow line.
 * Fire-and-forget to Go; Go echoes back via double-links trace event. */
export function DoubleLinksToggle() {
  const active = useCameraStore((s) => s.doubleLinksVisible);
  const onClick = useCallback((e: React.MouseEvent) => {
    e.stopPropagation();
    // Fire-and-forget: Go owns the toggle state and echoes back via double-links.
    postLog("guide-btn-click", { op: "double-links", was: active });
    vscode.postMessage({ type: "edit", op: "double-links" });
  }, [active]);
  return (
    <div
      onClick={onClick}
      title={active ? "Hide double-link overlay" : "Show double-link overlay"}
      style={{
        position: "absolute",
        top: 316,
        right: 12,
        background: "rgba(0,0,0,0.55)",
        borderRadius: 6,
        padding: "3px 7px",
        cursor: "pointer",
        pointerEvents: "auto",
        zIndex: 20,
        color: active ? "#00e5ff" : "#888",
        fontSize: 11,
        fontFamily: "monospace",
        userSelect: "none",
        display: "flex",
        alignItems: "center",
        gap: 4,
      }}
    >
      ⇄ double links
    </div>
  );
}

/** BADGES TOGGLE: top-right button to show/hide occlusion +N badges.
 *  Reads badgesHidden from camera-store (Go-owned via badges-global trace events).
 *  Click dispatches fire-and-forget "badges-vis" to Go; Go echoes back the new state.
 */
export function BadgesToggle() { return <Toggle cfg={badgesCfg} />; }
