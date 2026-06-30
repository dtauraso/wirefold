// camera-ui.tsx — standalone camera control UI widgets for ThreeView.
// OverlaysControl (split-button + popover), HomeButton, DoubleLinksToggle — no scene/Go logic.

import React, { useCallback, useState } from "react";
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

function fireToggle(cfg: ToggleCfg, val: boolean) {
  postLog("guide-btn-click", cfg.payload(val));
  vscode.postMessage({ type: "edit", op: cfg.op } as Parameters<typeof vscode.postMessage>[0]);
}

// ---------------------------------------------------------------------------
// Config table for the 9 toggle buttons
// ---------------------------------------------------------------------------

const guidelinesCfg: ToggleCfg = {
  op: "overlays-vis",
  selector: (s) => s.overlaysVisible,
  active: (v) => v,
  label: "▦ overlays",
  title: (a) => (a ? "Hide overlays" : "Show overlays"),
  payload: (v) => ({ op: "overlays-vis", was: v }),
};

const ringsCfg: ToggleCfg = {
  op: "tori-vis",
  selector: (s) => s.sceneToriVisible,
  active: (v) => v,
  label: "◎ rings",
  title: (a) => (a ? "Hide polar rings" : "Show polar rings"),
  payload: (v) => ({ op: "tori-vis", was: v }),
};

const scenePolesCfg: ToggleCfg = {
  op: "scene-poles",
  selector: (s) => s.scenePolesVisible,
  active: (v) => v,
  label: "⊹ scene poles",
  title: (a) => (a ? "Hide scene pole frame" : "Show scene pole frame"),
  payload: (v) => ({ op: "scene-poles", was: v }),
};

const nodePolesCfg: ToggleCfg = {
  op: "node-poles",
  selector: (s) => s.nodePolesVisible,
  active: (v) => v,
  label: "⊹ node poles",
  title: (a) => (a ? "Hide node pole frames" : "Show node pole frames"),
  payload: (v) => ({ op: "node-poles", was: v }),
};

const angleLabelsCfg: ToggleCfg = {
  op: "angle-labels",
  selector: (s) => s.angleLabelsVisible,
  active: (v) => v,
  label: "θφ 2→3/7",
  title: (a) => (a ? "Hide angle arcs+labels" : "Show angle arcs+labels"),
  payload: (v) => ({ op: "angle-labels", was: v }),
};

const selSpherePolesCfg: ToggleCfg = {
  op: "sel-sphere-poles",
  selector: (s) => s.selSpherePolesVisible,
  active: (v) => v,
  label: "sel ⬡",
  title: (a) => (a ? "Hide sel-sphere poles" : "Show sel-sphere poles"),
  payload: (v) => ({ op: "sel-sphere-poles", was: v }),
};

const handholdsCfg: ToggleCfg = {
  op: "handholds-vis",
  selector: (s) => s.handholdsVisible,
  active: (v) => v !== false,
  label: "⊙ grips",
  title: (a) => (a ? "Hide rotation grips" : "Show rotation grips"),
  payload: (v) => ({ op: "handholds-vis", was: v }),
};

const globalLabelsCfg: ToggleCfg = {
  op: "labels-vis",
  selector: (s) => s.labelsGlobalHidden,
  active: (v) => !v,
  label: (v) => `${v ? "▴" : "▾"} labels`,
  title: (a) => (a ? "Hide labels" : "Show labels"),
  payload: (v) => ({ op: "labels-vis", wasHidden: v }),
};

const badgesCfg: ToggleCfg = {
  op: "badges-vis",
  selector: (s) => s.badgesHidden,
  active: (v) => !v,
  label: (v) => `${v ? "▴" : "▾"} +N badges`,
  title: (a) => (a ? "Hide +N badges" : "Show +N badges"),
  payload: (v) => ({ op: "badges-vis", wasHidden: v }),
};

// ---------------------------------------------------------------------------
// Grouped overlay rows for the popover
// ---------------------------------------------------------------------------

type OverlayGroup = { heading: string; cfgs: ToggleCfg[] };

const OVERLAY_GROUPS: OverlayGroup[] = [
  { heading: "GUIDES", cfgs: [angleLabelsCfg, ringsCfg, handholdsCfg] },
  { heading: "POLES",  cfgs: [scenePolesCfg, nodePolesCfg, selSpherePolesCfg] },
  { heading: "LABELS", cfgs: [globalLabelsCfg, badgesCfg] },
];

/** A single row inside the popover: checkbox glyph + label, fires the row's op on click. */
function OverlayRow({ cfg }: { cfg: ToggleCfg }) {
  const val = useCameraStore(cfg.selector);
  const active = cfg.active(val);
  const onClick = useCallback(
    (e: React.MouseEvent) => {
      e.stopPropagation();
      fireToggle(cfg, val);
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [val]
  );
  const labelText = typeof cfg.label === "function" ? cfg.label(val) : cfg.label;
  return (
    <div
      onClick={onClick}
      title={cfg.title(active)}
      style={{
        display: "flex",
        alignItems: "center",
        gap: 6,
        padding: "3px 6px",
        cursor: "pointer",
        color: active ? "#ddd" : "#888",
        borderRadius: 4,
        userSelect: "none",
      }}
    >
      <span style={{ width: 10, textAlign: "center" }}>{active ? "✓" : " "}</span>
      <span>{labelText}</span>
    </div>
  );
}

/** OVERLAYS CONTROL: split-button (body = master toggle, caret = popover) + popover checklist. */
export function OverlaysControl() {
  const [open, setOpen] = useState(false);
  const val = useCameraStore(guidelinesCfg.selector);
  const active = guidelinesCfg.active(val);

  const onBodyClick = useCallback(
    (e: React.MouseEvent) => {
      e.stopPropagation();
      fireToggle(guidelinesCfg, val);
    },
    [val]
  );

  const onCaretClick = useCallback((e: React.MouseEvent) => {
    e.stopPropagation();
    setOpen((o) => !o);
  }, []);

  const cornerStyle: React.CSSProperties = {
    background: "rgba(0,0,0,0.55)",
    borderRadius: 6,
    fontSize: 11,
    fontFamily: "monospace",
    userSelect: "none",
  };

  return (
    <>
      {/* Split button */}
      <div
        style={{
          ...cornerStyle,
          position: "absolute",
          top: 76,
          right: 12,
          zIndex: 20,
          pointerEvents: "auto",
          display: "flex",
          alignItems: "stretch",
        }}
      >
        {/* Body — master toggle */}
        <div
          onClick={onBodyClick}
          title={guidelinesCfg.title(active)}
          style={{
            padding: "3px 7px",
            cursor: "pointer",
            color: active ? "#ddd" : "#888",
            display: "flex",
            alignItems: "center",
            gap: 4,
          }}
        >
          ▦ overlays
        </div>
        {/* Divider */}
        <div style={{ width: 1, background: "rgba(255,255,255,0.15)", margin: "3px 0" }} />
        {/* Caret — popover toggle */}
        <div
          onClick={onCaretClick}
          title={open ? "Close overlay list" : "Open overlay list"}
          style={{
            padding: "3px 6px",
            cursor: "pointer",
            color: open ? "#ddd" : "#888",
            display: "flex",
            alignItems: "center",
          }}
        >
          {open ? "▴" : "▾"}
        </div>
      </div>

      {/* Popover */}
      {open && (
        <div
          style={{
            ...cornerStyle,
            position: "absolute",
            top: 104,
            right: 12,
            zIndex: 21,
            pointerEvents: "auto",
            minWidth: 140,
            padding: "4px 0",
          }}
        >
          {OVERLAY_GROUPS.map((group) => (
            <div key={group.heading}>
              <div
                style={{
                  padding: "4px 8px 2px",
                  fontSize: 9,
                  color: "#666",
                  letterSpacing: "0.08em",
                  userSelect: "none",
                }}
              >
                {group.heading}
              </div>
              {group.cfgs.map((cfg) => (
                <OverlayRow key={cfg.op} cfg={cfg} />
              ))}
            </div>
          ))}
        </div>
      )}
    </>
  );
}

// ---------------------------------------------------------------------------
// Widgets: Home button
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

