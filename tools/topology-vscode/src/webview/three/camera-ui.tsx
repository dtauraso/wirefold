// camera-ui.tsx — standalone camera control UI widgets for ThreeView.
// OverlaysControl (split-button + popover), HomeButton — no scene/Go logic.

import React, { useCallback, useState } from "react";
import * as THREE from "three";
import { postGoRecord } from "../vscode-api";
import { encodeOverlaysToggle } from "../../schema/input-layout";
import type { OverlayFlag } from "../../messages";
import { postLog } from "../log/post";
import { useOverlayFlags } from "./overlay-flags";
import { sendRawInput, buildHomeRaw } from "./raw-input";

// ---------------------------------------------------------------------------
// Shared Toggle component
// ---------------------------------------------------------------------------

type ToggleCfg = {
  flag: OverlayFlag;
  /** Initial value shown before the first buffer snapshot lands (store polarity). */
  default: boolean;
  /** Compute active (highlight) from the raw value. */
  active: (val: boolean) => boolean;
  /** Label string or function of raw value. */
  label: string | ((val: boolean) => string);
  /** Title string function of active value. */
  title: (active: boolean) => string;
  /** postLog payload factory. */
  payload: (val: boolean) => Record<string, unknown>;
};

function fireToggle(cfg: ToggleCfg, val: boolean) {
  postLog("guide-btn-click", cfg.payload(val));
  postGoRecord(encodeOverlaysToggle(cfg.flag));
}

/** The value a toggle displays: the Go-owned Overlay buffer columns (the only live truth).
 *  cfg.flag keys the buffer record in store polarity. Falls back to cfg.default until the
 *  first snapshot lands. */
function useToggleVal(cfg: ToggleCfg): boolean {
  const bufFlags = useOverlayFlags();
  // ?? cfg.default only guards the (impossible) missing-key case under noUncheckedIndexedAccess;
  // every OverlayFlag is always present in the record, so `false` is preserved.
  if (bufFlags) return bufFlags[cfg.flag] ?? cfg.default;
  return cfg.default;
}

// ---------------------------------------------------------------------------
// Config table for the 9 toggle buttons
// ---------------------------------------------------------------------------

const guidelinesCfg: ToggleCfg = {
  flag: "overlays",
  default: true,
  active: (v) => v,
  label: "▦ overlays",
  title: (a) => (a ? "Hide overlays" : "Show overlays"),
  payload: (v) => ({ flag: "overlays", was: v }),
};

const ringsCfg: ToggleCfg = {
  flag: "tori",
  default: true,
  active: (v) => v,
  label: "◎ rings",
  title: (a) => (a ? "Hide polar rings" : "Show polar rings"),
  payload: (v) => ({ flag: "tori", was: v }),
};

const scenePolesCfg: ToggleCfg = {
  flag: "scenePoles",
  default: true,
  active: (v) => v,
  label: "⊹ scene poles",
  title: (a) => (a ? "Hide scene pole frame" : "Show scene pole frame"),
  payload: (v) => ({ flag: "scenePoles", was: v }),
};

const nodePolesCfg: ToggleCfg = {
  flag: "nodePoles",
  default: true,
  active: (v) => v,
  label: "⊹ node poles",
  title: (a) => (a ? "Hide node pole frames" : "Show node pole frames"),
  payload: (v) => ({ flag: "nodePoles", was: v }),
};

const angleLabelsCfg: ToggleCfg = {
  flag: "angleLabels",
  default: true,
  active: (v) => v,
  label: "θφ 2→3/7",
  title: (a) => (a ? "Hide angle arcs+labels" : "Show angle arcs+labels"),
  payload: (v) => ({ flag: "angleLabels", was: v }),
};

const selSpherePolesCfg: ToggleCfg = {
  flag: "selSpherePoles",
  default: true,
  active: (v) => v,
  label: "select ⬡",
  title: (a) => (a ? "Hide select-sphere poles" : "Show select-sphere poles"),
  payload: (v) => ({ flag: "selSpherePoles", was: v }),
};

const handholdsCfg: ToggleCfg = {
  flag: "handholds",
  default: true,
  active: (v) => v !== false,
  label: "⊙ grips",
  title: (a) => (a ? "Hide rotation grips" : "Show rotation grips"),
  payload: (v) => ({ flag: "handholds", was: v }),
};

const globalLabelsCfg: ToggleCfg = {
  flag: "labelsGlobal",
  default: false,
  active: (v) => !v,
  label: (v) => `${v ? "▴" : "▾"} labels`,
  title: (a) => (a ? "Hide labels" : "Show labels"),
  payload: (v) => ({ flag: "labelsGlobal", wasHidden: v }),
};

const badgesCfg: ToggleCfg = {
  flag: "badgesGlobal",
  default: false,
  active: (v) => !v,
  label: (v) => `${v ? "▴" : "▾"} +N badges`,
  title: (a) => (a ? "Hide +N badges" : "Show +N badges"),
  payload: (v) => ({ flag: "badgesGlobal", wasHidden: v }),
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
  aspect,
}: {
  cameraRef: React.MutableRefObject<THREE.PerspectiveCamera | null>;
  aspect: number;
}) {
  const onClick = useCallback((e: React.MouseEvent) => {
    e.stopPropagation();
    const cam = cameraRef.current;
    if (!cam) return;
    // Home is a COMMAND to Go. TS sends only render context (fov + aspect); Go frames the
    // scene from its OWN node geometry, installs the pose in the gesture FSM, and streams it
    // back via the buffer's Camera row (BufferCamera). Because the FSM's own pose becomes the
    // framed pose, the next pan/zoom/rotate builds on it (no snap-back). We do NOT mutate the
    // three.js camera or seed a computed pose here.
    sendRawInput(buildHomeRaw(cam.fov, aspect));
  }, [cameraRef, aspect]);

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


