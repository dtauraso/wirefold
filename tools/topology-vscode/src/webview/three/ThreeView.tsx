// ThreeView — 3D replica of the RF graph with interaction grammar:
//   - PerspectiveCamera (parallax + depth cues)
//   - Click → raycast pick/select
//   - Node drag → move node on z=0 plane; drop on another node → create edge
//   - Two-finger scroll → pan; pinch → dolly along view axis
//   - Roll slider widget (screen-plane camera roll)

import { useEffect, useRef, useState, useCallback, useMemo } from "react";
import { Canvas } from "@react-three/fiber";
import * as THREE from "three";
import type { RFNode, NodeData, EdgeData } from "../types";
import type { RFEdge } from "../types";
import { useThreeStore } from "./store";
import { pixelToNDC } from "./geometry-helpers";
import { HomeButton, OverlaysControl } from "./camera-ui";
import { useCameraStore } from "./camera-store";
import { useInteractionControls } from "./interaction-controls";
import type { PickOptions } from "./interaction-controls";
import { Scene } from "./scene-content";
import { BufferScene, BufferLabelProjector, USE_BUFFER_RENDER } from "./buffer-scene";
import type { BufferLabelPos } from "./buffer-scene";
import { computeOcclusionCounts, computeOcclusionCountsNav } from "./scene-occlusion";
import { getLatestSnapshot } from "../snapshot-buffer";
import { decodeSnapshot } from "./buffer-decode";
import { decodeNavNodes, getNavNodeIds } from "./buffer-nav";
import { NavGuides } from "./NavGuides";
import { PanPolarOverlay } from "./PanPolarOverlay";
import { viewerState, patchViewerState } from "../state/viewer-state";
import { scheduleViewSave } from "../save";
import { USE_NEW_SYSTEM } from "../new-system";

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
  const nodes = useThreeStore((s) => s.nodes);
  const edges = useThreeStore((s) => s.edges);
  const loadError = useThreeStore((s) => s.loadError);
  const storeCreateEdge = useThreeStore((s) => s.createEdge);
  const storeDeleteEdge = useThreeStore((s) => s.deleteEdge);
  const toggleFade = useThreeStore((s) => s.toggleFade);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  // Which spheres to show on selection: "surface" (single click) = the spheres the
  // node sits on the surface of; "own" (two-finger click) = the node's own sphere.
  const [sphereMode, setSphereMode] = useState<"surface" | "own">("surface");
  const [hoveredId, setHoveredId] = useState<string | null>(null);
  const [labelPositions, setLabelPositions] = useState<{ id: string; px: number; py: number; cx: number; cy: number }[]>([]);
  // Buffer-driven label positions (new-system path): projected from the binary buffer's
  // node block + the buffer-nav id table by BufferLabelProjector. Separate from the
  // old-path labelPositions so flag-off stays byte-for-byte.
  const [bufferLabelPositions, setBufferLabelPositions] = useState<BufferLabelPos[]>([]);
  // globalLabelsHidden is Go-owned: written by pump on labels-global trace events.
  const globalLabelsHidden = useCameraStore((s) => s.labelsGlobalHidden);
  // badgesHidden is Go-owned: written by pump on badges-global trace events.
  const badgesHidden = useCameraStore((s) => s.badgesHidden);
  // Ref mirror of nodes — read in dolly/wheel to avoid stale closure.
  const nodesRef = useRef<RFNode<NodeData>[]>(nodes);
  // Ref mirror of edges — read in interaction-controls to compute incident edges
  // for the decentralized node-move dispatch (avoids stale closure).
  const edgesRef = useRef<RFEdge<EdgeData>[]>(edges);
  // Ref mirror of selectedId — read in interaction-controls to find arcball pivot.
  const selectedIdRef = useRef<string | null>(selectedId);

  const cameraRef = useRef<THREE.PerspectiveCamera | null>(null);
  // Persistent orbit/pan/dolly pivot in world space. NaN = uninitialized;
  // ensureTarget (interaction-controls) / Fit (camera-ui) seed it on first use.
  const targetRef = useRef<THREE.Vector3>(new THREE.Vector3(NaN, NaN, NaN));
  const pickRequest = useRef<((ndcX: number, ndcY: number, opts?: PickOptions) => string | null) | null>(null);
  const containerRef = useRef<HTMLDivElement | null>(null);
  const captureRef = useRef<HTMLDivElement | null>(null);
  const [canvasSize, setCanvasSize] = useState({ w: 800, h: 600 });

  // Keep refs in sync with state.
  useEffect(() => {
    nodesRef.current = nodes;
  }, [nodes]);
  useEffect(() => {
    edgesRef.current = edges;
  }, [edges]);
  useEffect(() => {
    selectedIdRef.current = selectedId;
  }, [selectedId]);

  // Observe container size
  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    const obs = new ResizeObserver(() => setCanvasSize({ w: el.clientWidth, h: el.clientHeight }));
    obs.observe(el);
    setCanvasSize({ w: el.clientWidth, h: el.clientHeight });
    return () => obs.disconnect();
  }, []);

  // Label positions are updated from inside the Canvas via useFrame (no state churn).
  // We use a ref-based callback that batches updates at ~60fps via requestAnimationFrame.
  const labelRaf = useRef<ReturnType<typeof requestAnimationFrame> | null>(null);
  const pendingPositions = useRef<{ id: string; px: number; py: number; cx: number; cy: number }[]>([]);
  const onPositions = useCallback((positions: { id: string; px: number; py: number; cx: number; cy: number }[]) => {
    pendingPositions.current = positions;
    if (labelRaf.current === null) {
      labelRaf.current = requestAnimationFrame(() => {
        setLabelPositions(pendingPositions.current);
        labelRaf.current = null;
      });
    }
  }, []);

  // Buffer-driven label positions — same RAF-batching as onPositions, separate buffer.
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
      if (labelRaf.current !== null) {
        cancelAnimationFrame(labelRaf.current);
        labelRaf.current = null;
      }
      if (bufferLabelRaf.current !== null) {
        cancelAnimationFrame(bufferLabelRaf.current);
        bufferLabelRaf.current = null;
      }
    };
  }, []);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      // Read selectedId and edges from refs so this listener never needs to
      // re-subscribe when they change — it subscribes exactly once.
      const selId = selectedIdRef.current;
      const mod = e.metaKey || e.ctrlKey;
      // "f": toggle fade on the selected element.
      if (e.key === "f" && !mod && selId) {
        const isEdge = edgesRef.current.some((ed) => ed.id === selId);
        toggleFade({ kind: isEdge ? "edge" : "node", id: selId });
      }
      // Delete / Backspace: remove the selected edge (nodes/ports ignored).
      if ((e.key === "Delete" || e.key === "Backspace") && !mod && selId) {
        const isEdge = edgesRef.current.some((ed) => ed.id === selId);
        if (isEdge) {
          e.preventDefault();
          storeDeleteEdge(selId);
          setSelectedId(null);
        }
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  // toggleFade and storeDeleteEdge are stable Zustand action selectors.
  // selectedId and edges are read via refs (selectedIdRef, edgesRef) so they
  // are excluded from deps — the listener subscribes once for the component lifetime.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [toggleFade, storeDeleteEdge]);

  // Route a pick result to selection state: a node/edge/port id, or null for empty space.
  const handleSelect = useCallback((id: string | null, ownSphere?: boolean) => {
    setSelectedId(id);
    setSphereMode(ownSphere ? "own" : "surface");
  }, []);

  const { onPointerDown, onPointerMove, onPointerUp, onWheelNative } = useInteractionControls(
    cameraRef,
    canvasSize,
    pickRequest,
    handleSelect,
    nodesRef,
    storeCreateEdge,
    selectedIdRef,
    edgesRef,
    targetRef,
  );

  // Bind wheel listener as non-passive so e.preventDefault() actually works.
  // React's synthetic onWheel is passive — preventDefault silently no-ops there,
  // which lets horizontal two-finger drags trigger browser back-nav.
  useEffect(() => {
    const el = captureRef.current;
    if (!el) return;
    el.addEventListener("wheel", onWheelNative, { passive: false });
    return () => el.removeEventListener("wheel", onWheelNative);
  }, [onWheelNative]);

  // Hover tracking: lightweight raycast on pointer-move to update hoveredId.
  const hoverRafRef = useRef<ReturnType<typeof requestAnimationFrame> | null>(null);
  const onPointerMoveWithHover = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      onPointerMove(e); // original interaction handling
      // Throttle hover raycasts via rAF.
      if (hoverRafRef.current !== null) return;
      const rect = (e.currentTarget).getBoundingClientRect();
      const clientX = e.clientX;
      const clientY = e.clientY;
      hoverRafRef.current = requestAnimationFrame(() => {
        hoverRafRef.current = null;
        if (!pickRequest.current) return;
        const { ndcX, ndcY } = pixelToNDC(clientX, clientY, rect);
        const hitId = pickRequest.current(ndcX, ndcY);
        setHoveredId(hitId);
      });
    },
    [onPointerMove, pickRequest],
  );

  const onPointerLeave = useCallback(() => {
    // Cancel any pending hover rAF to avoid phantom hover after pointer leaves.
    if (hoverRafRef.current !== null) {
      cancelAnimationFrame(hoverRafRef.current);
      hoverRafRef.current = null;
    }
    setHoveredId(null);
  }, []);

  // Occlusion counts: recomputed only when the camera settles (not per-frame).
  // Map from frontNodeId → N (nodes hidden directly behind it from current viewpoint).
  const [occlusionCounts, setOcclusionCounts] = useState<Map<string, number>>(new Map());
  // Buffer-driven occlusion counts (new-system path), computed from the buffer's node
  // block on camera-settle. Separate from occlusionCounts so flag-off stays unchanged.
  const [bufferOcclusionCounts, setBufferOcclusionCounts] = useState<Map<string, number>>(new Map());

  const onCameraSettle = useCallback(() => {
    const cam = cameraRef.current;
    if (!cam) return;
    if (USE_NEW_SYSTEM) {
      // Buffer-driven: decode the latest snapshot's node block + id table and compute
      // occlusion from Go-owned centers/radii (never the RFNode array).
      const snap = getLatestSnapshot();
      const decoded = snap ? decodeSnapshot(snap) : null;
      const nav = decoded ? decodeNavNodes(decoded, getNavNodeIds()) : [];
      setBufferOcclusionCounts(computeOcclusionCountsNav(nav, cam, canvasSize));
      return;
    }
    const counts = computeOcclusionCounts(nodes, cam, canvasSize);
    setOcclusionCounts(counts);
  }, [nodes, cameraRef, canvasSize]);

  const labelMap = useMemo(() => new Map(labelPositions.map((p) => [p.id, p])), [labelPositions]);
  const bufferLabelMap = useMemo(() => new Map(bufferLabelPositions.map((p) => [p.id, p])), [bufferLabelPositions]);
  // Node label text keyed by id (from the loaded topology; the buffer is numeric).
  // Used by the new-system pills to render human labels for each buffer node row.
  const nodeLabelMap = useMemo(() => new Map(nodes.map((n) => [n.id, n.data?.label ?? n.id])), [nodes]);


  return (
    <div ref={containerRef} style={{ position: "absolute", inset: 0 }}>
      {/* Canvas + gesture capture layer */}
      <div
        ref={captureRef}
        style={{ position: "absolute", inset: 0, touchAction: "none" }}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMoveWithHover}
        onPointerUp={onPointerUp}
        onPointerLeave={onPointerLeave}
        onContextMenu={(e) => e.preventDefault()}
      >
        <Canvas
          camera={{ fov: 50, near: 0.1, far: 20000, position: [0, 0, 500] }}
          gl={{ antialias: true }}
          style={{ position: "absolute", inset: 0 }}
          frameloop="always"
        >
          <Scene
            nodes={nodes}
            edges={edges}
            selectedId={selectedId}
            sphereMode={sphereMode}
            hoveredId={hoveredId}
            cameraRef={cameraRef}
            initialCamera3d={viewerState.camera3d}
            initialCameraPolar={viewerState.cameraPolar}
            onPickRequest={pickRequest}
            onPositions={onPositions}
            onCameraSettle={onCameraSettle}
          />
          {/* NavGuides (polar tori / pole frames / θ-φ angle arcs / handholds). Under the
              new-system flag its geometry now derives from the binary buffer (Go-owned
              node centers/radii/sphereR + Go-owned selection column) via buffer-nav; the
              flag-off path still reads the RFNode array + node-geometry store. So it is
              mounted UNCONDITIONALLY — the data source is gated inside NavGuides, not the
              mount. (The `nodes`/`selectedId` props are consumed only on the flag-off
              path; flag-on ignores them and reads the buffer.) */}
          <NavGuides nodes={nodes} selectedId={selectedId} />
          {USE_BUFFER_RENDER && <BufferScene cameraRef={cameraRef} />}
          {USE_BUFFER_RENDER && <BufferLabelProjector onPositions={onBufferPositions} />}
        </Canvas>
      </div>

      {/* Label overlay — real camera projection, updated every frame.
          All nodes with a projected position render their label (subject to the global toggle). */}
      {/* Node label pills — FLAG-OFF path: projected by LabelProjector over the RFNode
          array. Byte-for-byte unchanged. The new-system path renders the equivalent
          pills below from the buffer projection + id table. */}
      {!USE_NEW_SYSTEM && !globalLabelsHidden && nodes.map((n) => {
        const pos = labelMap.get(n.id);
        if (!pos) return null;
        return (
          <div
            key={n.id}
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
            <div style={{ whiteSpace: "nowrap" }}>{n.data?.label ?? n.id}</div>
          </div>
        );
      })}

      {/* Node label pills — NEW-SYSTEM path: one pill per buffer-projected node position
          (BufferLabelProjector), label text looked up from the id table via nodeLabelMap.
          Same style as the flag-off pills; positions/geometry are buffer-driven. */}
      {USE_NEW_SYSTEM && !globalLabelsHidden && bufferLabelPositions.map((pos) => (
        <div
          key={pos.id}
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
          <div style={{ whiteSpace: "nowrap" }}>{nodeLabelMap.get(pos.id) ?? pos.id}</div>
        </div>
      ))}

      {/* Occlusion count badges — "+N" pill at top-right of front node's projected center.
          Only shown when N >= 1. Recomputed on camera settle (not per-frame).
          Full occlusion is allowed — layout never moves (honesty preserved).
          TODO(3d): large-count cap/format deferred */}
      {/* Occlusion "+N" badges — FLAG-OFF path (occlusion computed over the RFNode array
          + old label projection). Byte-for-byte unchanged. New-system badges below. */}
      {!USE_NEW_SYSTEM && !badgesHidden && nodes.map((n) => {
        const count = occlusionCounts.get(n.id);
        if (!count || count < 1) return null;
        const pos = labelMap.get(n.id);
        if (!pos) return null;
        return (
          <div
            key={`badge-${n.id}`}
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

      {/* Occlusion "+N" badges — NEW-SYSTEM path: occlusion computed from the buffer's
          node block (computeOcclusionCountsNav), positioned at the buffer projection.
          Same style as the flag-off badges; fully buffer-driven. */}
      {USE_NEW_SYSTEM && !badgesHidden && bufferLabelPositions.map((pos) => {
        const count = bufferOcclusionCounts.get(pos.id);
        if (!count || count < 1) return null;
        return (
          <div
            key={`badge-${pos.id}`}
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
      <HomeButton cameraRef={cameraRef} nodesRef={nodesRef} targetRef={targetRef} aspect={canvasSize.w / canvasSize.h} />
      <OverlaysControl />

      {/* Polar pan overlay — "mouse as polar" construction during a wheel-pan burst */}
      <PanPolarOverlay />

      {/* Load-error banner: shown when store.load() throws (parse failure). Blank
          diagram + this banner means the spec file is malformed; check .probe. */}
      {loadError && (
        <div style={{
          position: "absolute", top: 8, left: "50%", transform: "translateX(-50%)",
          background: "rgba(180,40,40,0.92)", color: "#fff", borderRadius: 4,
          padding: "6px 14px", fontSize: 12, pointerEvents: "none", zIndex: 100,
          maxWidth: "80%", textAlign: "center",
        }}>
          Load failed: {loadError}
        </div>
      )}

    </div>
  );
}
