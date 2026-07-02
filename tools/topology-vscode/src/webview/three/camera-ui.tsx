// camera-ui.tsx — standalone camera control UI widgets for ThreeView.
// OverlaysControl (split-button + popover), HomeButton — no scene/Go logic.

import React, { useCallback, useState } from "react";
import * as THREE from "three";
import type { RFNode, NodeData } from "../types";
import { boundingBox3D, fitDistance } from "./geometry-helpers";
import { vscode } from "../vscode-api";
import type { OverlayFlag } from "../../messages";
import { useCameraStore } from "./camera-store";
import { postLog } from "../log/post";
import { commitCamera } from "./interaction-handlers";
import { USE_NEW_SYSTEM } from "../new-system";
import { useOverlayFlags } from "./overlay-flags";
import { sendRawInput, buildHomeRaw } from "./raw-input";

// ---------------------------------------------------------------------------
// Shared Toggle component
// ---------------------------------------------------------------------------

type ToggleCfg = {
  flag: OverlayFlag;
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
  vscode.postMessage({ type: "edit", op: "update", kind: "overlays", attr: "toggle", flag: cfg.flag });
}

/** The value a toggle displays. Old path: useCameraStore (pump-fed). New path: the
 *  Go-owned Overlay buffer columns — pump is gated off there so the store is inert, and
 *  the buffer is the only live truth. Both hooks run unconditionally (stable order); the
 *  buffer value wins under the flag once the first snapshot has landed, otherwise the
 *  store default is a fine initial. cfg.flag keys the buffer record in store polarity. */
function useToggleVal(cfg: ToggleCfg): boolean {
  const storeVal = useCameraStore(cfg.selector);
  const bufFlags = useOverlayFlags();
  // ?? storeVal only guards the (impossible) missing-key case under noUncheckedIndexedAccess;
  // every OverlayFlag is always present in the record, so `false` is preserved.
  if (USE_NEW_SYSTEM && bufFlags) return bufFlags[cfg.flag] ?? storeVal;
  return storeVal;
}

// ---------------------------------------------------------------------------
// Config table for the 9 toggle buttons
// ---------------------------------------------------------------------------

const guidelinesCfg: ToggleCfg = {
  flag: "overlays",
  selector: (s) => s.overlaysVisible,
  active: (v) => v,
  label: "▦ overlays",
  title: (a) => (a ? "Hide overlays" : "Show overlays"),
  payload: (v) => ({ flag: "overlays", was: v }),
};

const ringsCfg: ToggleCfg = {
  flag: "tori",
  selector: (s) => s.sceneToriVisible,
  active: (v) => v,
  label: "◎ rings",
  title: (a) => (a ? "Hide polar rings" : "Show polar rings"),
  payload: (v) => ({ flag: "tori", was: v }),
};

const scenePolesCfg: ToggleCfg = {
  flag: "scenePoles",
  selector: (s) => s.scenePolesVisible,
  active: (v) => v,
  label: "⊹ scene poles",
  title: (a) => (a ? "Hide scene pole frame" : "Show scene pole frame"),
  payload: (v) => ({ flag: "scenePoles", was: v }),
};

const nodePolesCfg: ToggleCfg = {
  flag: "nodePoles",
  selector: (s) => s.nodePolesVisible,
  active: (v) => v,
  label: "⊹ node poles",
  title: (a) => (a ? "Hide node pole frames" : "Show node pole frames"),
  payload: (v) => ({ flag: "nodePoles", was: v }),
};

const angleLabelsCfg: ToggleCfg = {
  flag: "angleLabels",
  selector: (s) => s.angleLabelsVisible,
  active: (v) => v,
  label: "θφ 2→3/7",
  title: (a) => (a ? "Hide angle arcs+labels" : "Show angle arcs+labels"),
  payload: (v) => ({ flag: "angleLabels", was: v }),
};

const selSpherePolesCfg: ToggleCfg = {
  flag: "selSpherePoles",
  selector: (s) => s.selSpherePolesVisible,
  active: (v) => v,
  label: "sel ⬡",
  title: (a) => (a ? "Hide sel-sphere poles" : "Show sel-sphere poles"),
  payload: (v) => ({ flag: "selSpherePoles", was: v }),
};

const handholdsCfg: ToggleCfg = {
  flag: "handholds",
  selector: (s) => s.handholdsVisible,
  active: (v) => v !== false,
  label: "⊙ grips",
  title: (a) => (a ? "Hide rotation grips" : "Show rotation grips"),
  payload: (v) => ({ flag: "handholds", was: v }),
};

const globalLabelsCfg: ToggleCfg = {
  flag: "labelsGlobal",
  selector: (s) => s.labelsGlobalHidden,
  active: (v) => !v,
  label: (v) => `${v ? "▴" : "▾"} labels`,
  title: (a) => (a ? "Hide labels" : "Show labels"),
  payload: (v) => ({ flag: "labelsGlobal", wasHidden: v }),
};

const badgesCfg: ToggleCfg = {
  flag: "badgesGlobal",
  selector: (s) => s.badgesHidden,
  active: (v) => !v,
  label: (v) => `${v ? "▴" : "▾"} +N badges`,
  title: (a) => (a ? "Hide +N badges" : "Show +N badges"),
  payload: (v) => ({ flag: "badgesGlobal", wasHidden: v }),
};

const doubleLinksCfg: ToggleCfg = {
  flag: "doubleLinks",
  selector: (s) => s.doubleLinksVisible,
  active: (v) => v,
  label: "⇄ double links",
  title: (a) => (a ? "Hide double-link overlay" : "Show double-link overlay"),
  payload: (v) => ({ flag: "doubleLinks", was: v }),
};

// ---------------------------------------------------------------------------
// Grouped overlay rows for the popover
// ---------------------------------------------------------------------------

type OverlayGroup = { heading: string; cfgs: ToggleCfg[] };

const OVERLAY_GROUPS: OverlayGroup[] = [
  { heading: "GUIDES", cfgs: [angleLabelsCfg, ringsCfg, handholdsCfg] },
  { heading: "POLES",  cfgs: [scenePolesCfg, nodePolesCfg, selSpherePolesCfg] },
  { heading: "LABELS", cfgs: [globalLabelsCfg, badgesCfg] },
  { heading: "EDGES",  cfgs: [doubleLinksCfg] },
];

/** A single row inside the popover: square checkbox + label, fires the row's op on click.
 *  Styled to match the recommended mock (overlay-toggle-options.html): custom .cb checkbox
 *  that fills accent + ✓ when checked, with a subtle row-hover background. */
function OverlayRow({ cfg, disabled }: { cfg: ToggleCfg; disabled?: boolean }) {
  const val = useToggleVal(cfg);
  const active = cfg.active(val);
  const [hover, setHover] = useState(false);
  const onClick = useCallback(
    (e: React.MouseEvent) => {
      e.stopPropagation();
      if (disabled) return;
      fireToggle(cfg, val);
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [val, disabled]
  );
  const labelText = typeof cfg.label === "function" ? cfg.label(val) : cfg.label;
  return (
    <div
      onClick={onClick}
      onMouseEnter={() => setHover(true)}
      onMouseLeave={() => setHover(false)}
      title={cfg.title(active)}
      style={{
        display: "flex",
        alignItems: "center",
        gap: 7,
        padding: "4px 6px",
        cursor: disabled ? "default" : "pointer",
        color: "#e7e7ea",
        borderRadius: 5,
        background: !disabled && hover ? "rgba(255,255,255,0.05)" : "transparent",
        userSelect: "none",
        fontSize: 11.5,
      }}
    >
      <span
        style={{
          width: 13,
          height: 13,
          flex: "0 0 auto",
          borderRadius: 3,
          border: `1.5px solid ${active ? "#4ea1ff" : "#9a9aa6"}`,
          background: active ? "#4ea1ff" : "transparent",
          display: "grid",
          placeItems: "center",
          color: "#04101f",
          fontSize: 10,
          fontWeight: 900,
          lineHeight: "11px",
        }}
      >
        {active ? "✓" : ""}
      </span>
      <span>{labelText}</span>
    </div>
  );
}

/** OVERLAYS CONTROL: split-button (body = master toggle, caret = popover) + popover checklist. */
export function OverlaysControl() {
  const [open, setOpen] = useState(false);
  const val = useToggleVal(guidelinesCfg);
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

  const fontStack = '-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif';

  return (
    <>
      {/* Split button — labeled pill (body = master toggle, caret = popover). Accent fill
          when the master is on, neutral chip when off (overlay-toggle-options.html mock). */}
      <div
        style={{
          position: "absolute",
          top: 76,
          right: 12,
          zIndex: 20,
          pointerEvents: "auto",
          display: "flex",
          alignItems: "stretch",
          borderRadius: 6,
          overflow: "hidden",
          fontSize: 11,
          fontWeight: 600,
          fontFamily: fontStack,
          background: active ? "#4ea1ff" : "#34343d",
          border: `1px solid ${active ? "#4ea1ff" : "#3a3a44"}`,
          color: active ? "#04101f" : "#9a9aa6",
          userSelect: "none",
        }}
      >
        {/* Body — master toggle */}
        <div
          onClick={onBodyClick}
          title={guidelinesCfg.title(active)}
          style={{ padding: "3px 9px", cursor: "pointer", display: "flex", alignItems: "center" }}
        >
          Overlays
        </div>
        {/* Caret — popover toggle */}
        <div
          onClick={onCaretClick}
          title={open ? "Close overlay list" : "Open overlay list"}
          style={{
            padding: "3px 7px 3px 4px",
            cursor: "pointer",
            display: "flex",
            alignItems: "center",
            fontSize: 9,
            opacity: 0.85,
          }}
        >
          {open ? "▴" : "▾"}
        </div>
      </div>

      {/* Popover — grouped checklist (.pop mock style: panel2 bg, border, shadow). */}
      {open && (
        <div
          style={{
            position: "absolute",
            top: 104,
            right: 12,
            zIndex: 21,
            pointerEvents: "auto",
            width: 150,
            background: "#2f2f37",
            border: "1px solid #3a3a44",
            borderRadius: 8,
            padding: 6,
            boxShadow: "0 8px 24px rgba(0,0,0,0.5)",
            fontFamily: fontStack,
            userSelect: "none",
          }}
        >
          <div style={{ opacity: active ? 1 : 0.4, transition: "opacity 0.12s ease" }}>
            {OVERLAY_GROUPS.map((group) => (
              <div key={group.heading}>
                <div
                  style={{
                    fontSize: 9.5,
                    textTransform: "uppercase",
                    letterSpacing: "0.05em",
                    color: "#9a9aa6",
                    padding: "6px 6px 2px",
                  }}
                >
                  {group.heading}
                </div>
                {group.cfgs.map((cfg) => (
                  <OverlayRow key={cfg.flag} cfg={cfg} disabled={!active} />
                ))}
              </div>
            ))}
          </div>
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

    // New system: Home is a COMMAND to Go. TS sends only render context (fov + aspect);
    // Go frames the scene from its OWN node geometry, installs the pose in the gesture
    // FSM, and streams it back (pump → useCameraStore → CameraFromStore). Because the
    // FSM's own pose becomes the framed pose, the next pan/zoom/rotate builds on it (no
    // snap-back). We do NOT mutate the three.js camera or seed a computed pose here.
    if (USE_NEW_SYSTEM) {
      sendRawInput(buildHomeRaw(cam.fov, aspect));
      return;
    }

    const { center, sizeX, sizeY, sizeZ } = boundingBox3D(nodes);
    const dist = fitDistance(cam.fov, aspect, sizeX, sizeY) + sizeZ / 2;
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


