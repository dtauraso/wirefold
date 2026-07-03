// ThreeView — 3D view of the Go-owned network, rendered entirely from the binary content
// buffer (BufferScene) with raw-input forwarding to Go's gesture FSM for all interaction.
//   - PerspectiveCamera driven by Go's Camera buffer row (BufferCamera)
//   - Pointer/wheel events forwarded raw to Go (useInteractionControls → raw-input)
//   - Node labels / +N occlusion badges projected from the buffer node block

import { useEffect, useRef, useState, useCallback } from "react";
import { Canvas } from "@react-three/fiber";
import * as THREE from "three";
import { HomeButton, OverlaysControl } from "./camera-ui";
import { useInteractionControls } from "./interaction-controls";
import type { PickOptions } from "./interaction-controls";
import { Scene } from "./scene-content";
import { BufferScene, BufferLabelProjector } from "./buffer-scene";
import { ProceduralEnvProvider } from "./scene-env";
import type { BufferLabelPos } from "./buffer-scene";
import { computeOcclusionCountsNav } from "./scene-occlusion";
import { getLatestSnapshot } from "../snapshot-buffer";
import { decodeSnapshot } from "./buffer-decode";
import { decodeNavNodes } from "./buffer-nav";
import { readOverlayLabelsGlobal, readOverlayBadgesGlobal } from "../../schema/buffer-layout";
import { NavGuides } from "./NavGuides";
import { postGoRecord } from "../vscode-api";
import { encodeFadeToggle } from "../../schema/input-layout";

// ---------------------------------------------------------------------------
// ThreeView: Canvas wrapper + interaction + label overlay + widgets
// ---------------------------------------------------------------------------

// Static label-pill style — no per-node data, so it is hoisted to module scope
// rather than reallocated per node per render.
const PILL_STYLE: React.CSSProperties = {
  background: "rgba(0,0,0,0.55)",
  border: "none",
  borderRadius: 4,
  padding: "3px 6px",
};

export function ThreeView() {
  // Buffer-driven label positions: projected from the binary buffer's node block + the
  // buffer-nav id table by BufferLabelProjector.
  const [bufferLabelPositions, setBufferLabelPositions] = useState<BufferLabelPos[]>([]);

  const cameraRef = useRef<THREE.PerspectiveCamera | null>(null);
  const pickRequest = useRef<((ndcX: number, ndcY: number, opts?: PickOptions) => string | null) | null>(null);
  const containerRef = useRef<HTMLDivElement | null>(null);
  const captureRef = useRef<HTMLDivElement | null>(null);
  const [canvasSize, setCanvasSize] = useState({ w: 800, h: 600 });

  // Observe container size
  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    const obs = new ResizeObserver(() => setCanvasSize({ w: el.clientWidth, h: el.clientHeight }));
    obs.observe(el);
    setCanvasSize({ w: el.clientWidth, h: el.clientHeight });
    return () => obs.disconnect();
  }, []);

  // Buffer-driven label positions — RAF-batched so state churns at most once per frame.
  const bufferLabelRaf = useRef<ReturnType<typeof requestAnimationFrame> | null>(null);
  const pendingBufferPositions = useRef<BufferLabelPos[]>([]);
  const onBufferPositions = useCallback((positions: BufferLabelPos[]) => {
    pendingBufferPositions.current = positions;
    if (bufferLabelRaf.current === null) {
      bufferLabelRaf.current = requestAnimationFrame(() => {
        setBufferLabelPositions(pendingBufferPositions.current);
        bufferLabelRaf.current = null;
      });
    }
  }, []);

  // Cancel any pending label RAF on unmount so it can't fire against a torn-down component.
  useEffect(() => {
    return () => {
      if (bufferLabelRaf.current !== null) {
        cancelAnimationFrame(bufferLabelRaf.current);
        bufferLabelRaf.current = null;
      }
    };
  }, []);

  const { onPointerDown, onPointerMove, onPointerUp, onWheelNative } = useInteractionControls(
    cameraRef,
    pickRequest,
  );

  // "f" toggles fade on Go's CURRENT selection (pre-branch ThreeView "f" handler). The
  // press is a BARE command — Go owns selection + topology and resolves which node/edge is
  // selected, flips its fade seed, and re-streams the Faded columns. Fire-and-forget; TS
  // holds no fade state. Ignored while typing in an input/textarea.
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key !== "f" && e.key !== "F") return;
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      const t = e.target as HTMLElement | null;
      const tag = t?.tagName;
      if (tag === "INPUT" || tag === "TEXTAREA" || t?.isContentEditable) return;
      postGoRecord(encodeFadeToggle());
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  // Bind wheel listener as non-passive so e.preventDefault() actually works.
  // React's synthetic onWheel is passive — preventDefault silently no-ops there,
  // which lets horizontal two-finger drags trigger browser back-nav.
  useEffect(() => {
    const el = captureRef.current;
    if (!el) return;
    el.addEventListener("wheel", onWheelNative, { passive: false });
    return () => el.removeEventListener("wheel", onWheelNative);
  }, [onWheelNative]);

  // Occlusion counts: recomputed only when the camera settles (not per-frame). Computed from
  // the buffer's node block (Go-owned centers/radii), never an RFNode array.
  const [bufferOcclusionCounts, setBufferOcclusionCounts] = useState<Map<number, number>>(new Map());

  const onCameraSettle = useCallback(() => {
    const cam = cameraRef.current;
    if (!cam) return;
    const snap = getLatestSnapshot();
    const decoded = snap ? decodeSnapshot(snap) : null;
    const nav = decoded ? decodeNavNodes(decoded) : [];
    setBufferOcclusionCounts(computeOcclusionCountsNav(nav, cam, canvasSize));
  }, [cameraRef, canvasSize]);

  // Label/badge global visibility come from the buffer overlay columns (Go-owned). Read at
  // render time: ThreeView re-renders every frame (bufferLabelPositions updates each rAF), so
  // a toggle change — which Go reflects into the buffer overlay row — is picked up within a
  // frame. Buffer sense: LabelsGlobal/BadgesGlobal == 1 means VISIBLE, so hidden = (col === 0).
  let bufLabelsHidden = false;
  let bufBadgesHidden = false;
  {
    const snap = getLatestSnapshot();
    const decoded = snap ? decodeSnapshot(snap) : null;
    if (decoded) {
      bufLabelsHidden = readOverlayLabelsGlobal(decoded.overlayView) === 0;
      bufBadgesHidden = readOverlayBadgesGlobal(decoded.overlayView) === 0;
    }
  }

  return (
    <div ref={containerRef} style={{ position: "absolute", inset: 0 }}>
      {/* Canvas + gesture capture layer */}
      <div
        ref={captureRef}
        style={{ position: "absolute", inset: 0, touchAction: "none" }}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
        onContextMenu={(e) => e.preventDefault()}
      >
        <Canvas
          camera={{ fov: 50, near: 0.1, far: 20000, position: [0, 0, 500] }}
          gl={{ antialias: true }}
          style={{ position: "absolute", inset: 0 }}
          frameloop="always"
        >
          <Scene
            onPickRequest={pickRequest}
            onCameraSettle={onCameraSettle}
          />
          {/* NavGuides (polar tori / pole frames / θ-φ angle arcs / handholds), derived from
              the binary buffer (Go-owned node centers/radii/sphereR + selection column). */}
          <NavGuides />
          {/* BufferScene's node bodies use a glassy PMREM-lit meshPhysicalMaterial, so it needs
              the env texture. Scene's own ProceduralEnvProvider wraps only the lighting. */}
          <ProceduralEnvProvider>
            <BufferScene cameraRef={cameraRef} />
          </ProceduralEnvProvider>
          <BufferLabelProjector onPositions={onBufferPositions} />
        </Canvas>
      </div>

      {/* Node label pills — one pill per buffer-projected node position (BufferLabelProjector),
          label text decoded straight from the buffer's label section (pos.label). No sidecar. */}
      {!bufLabelsHidden && bufferLabelPositions.map((pos) => (
        <div
          key={pos.row}
          style={{
            position: "absolute",
            left: pos.px,
            top: pos.py - 4,
            transform: "translate(-50%, -100%)",
            fontSize: 11,
            fontFamily: "monospace",
            color: "#e0e0e0",
            pointerEvents: "none",
            lineHeight: 1.25,
            textAlign: "center",
            zIndex: 10,
            ...PILL_STYLE,
          }}
        >
          <div style={{ whiteSpace: "nowrap" }}>{pos.label || String(pos.row)}</div>
        </div>
      ))}

      {/* Occlusion "+N" badges — count hidden nodes directly behind a front node, computed from
          the buffer's node block (computeOcclusionCountsNav) at the buffer projection. Only
          shown when N >= 1. Recomputed on camera settle. */}
      {!bufBadgesHidden && bufferLabelPositions.map((pos) => {
        const count = bufferOcclusionCounts.get(pos.row);
        if (!count || count < 1) return null;
        return (
          <div
            key={`badge-${pos.row}`}
            style={{
              position: "absolute",
              left: pos.px + 10,
              top: pos.py - 18,
              background: "rgba(30,30,50,0.88)",
              color: "#7df",
              fontSize: 9,
              fontFamily: "monospace",
              fontWeight: "bold",
              padding: "1px 5px",
              borderRadius: 8,
              border: "1px solid rgba(100,180,255,0.5)",
              pointerEvents: "none",
              whiteSpace: "nowrap",
              zIndex: 15,
              lineHeight: "14px",
            }}
          >
            +{count}
          </div>
        );
      })}

      {/* Widgets — fixed corner, pointerEvents auto */}
      <HomeButton cameraRef={cameraRef} aspect={canvasSize.w / canvasSize.h} />
      <OverlaysControl />
    </div>
  );
}
