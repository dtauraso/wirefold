// ThreeView — 3D replica of the RF graph with interaction grammar:
//   - PerspectiveCamera (parallax + depth cues)
//   - Click → raycast pick/select
//   - Node drag → move node on z=0 plane; drop on another node → create edge
//   - Two-finger scroll → pan; pinch → dolly along view axis
//   - Roll slider widget (screen-plane camera roll)

import { useEffect, useRef, useState, useCallback } from "react";
import { Canvas } from "@react-three/fiber";
import * as THREE from "three";
import type { RFNode, NodeData, EdgeData } from "../types";
import type { RFEdge } from "../types";
import { useThreeStore } from "./store";
import { pixelToNDC } from "./geometry-helpers";
import { GlobalLabelsToggle, HomeButton } from "./camera-ui";
import { useInteractionControls } from "./interaction-controls";
import type { PickOptions } from "./interaction-controls";
import { Scene, computeOcclusionCounts } from "./scene-content";
import { nodeOverrideText } from "./node-override-text";
import { viewerState, patchViewerState } from "../state/viewer-state";
import { scheduleViewSave } from "../save";

// ---------------------------------------------------------------------------
// ThreeView: Canvas wrapper + interaction + label overlay + widgets
// ---------------------------------------------------------------------------

export function ThreeView() {
  const nodes = useThreeStore((s) => s.nodes);
  const edges = useThreeStore((s) => s.edges);
  const storeMoveNode = useThreeStore((s) => s.moveNode);
  const storeCreateEdge = useThreeStore((s) => s.createEdge);
  const storeDeleteEdge = useThreeStore((s) => s.deleteEdge);
  const toggleFade = useThreeStore((s) => s.toggleFade);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [hoveredId, setHoveredId] = useState<string | null>(null);
  const [nearestNIds, setNearestNIds] = useState<Set<string>>(new Set());
  const [labelPositions, setLabelPositions] = useState<{ id: string; px: number; py: number; cx: number; cy: number }[]>([]);
  const [globalLabelsHidden, setGlobalLabelsHidden] = useState<boolean>(
    () => viewerState.labelsGlobalHidden ?? false,
  );
  // Ref mirror of nodes — read in dolly/wheel to avoid stale closure.
  const nodesRef = useRef<RFNode<NodeData>[]>(nodes);
  // Ref mirror of edges — read in interaction-controls to compute incident edges
  // for the decentralized node-move dispatch (avoids stale closure).
  const edgesRef = useRef<RFEdge<EdgeData>[]>(edges);
  // Ref mirror of selectedId — read in interaction-controls to find arcball pivot.
  const selectedIdRef = useRef<string | null>(selectedId);

  const cameraRef = useRef<THREE.PerspectiveCamera | null>(null);
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

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const mod = e.metaKey || e.ctrlKey;
      // "f": toggle fade on the selected element.
      if (e.key === "f" && !mod && selectedId) {
        const isEdge = edges.some((ed) => ed.id === selectedId);
        toggleFade({ kind: isEdge ? "edge" : "node", id: selectedId });
      }
      // Delete / Backspace: remove the selected edge (nodes/ports ignored).
      if ((e.key === "Delete" || e.key === "Backspace") && !mod && selectedId) {
        const isEdge = edges.some((ed) => ed.id === selectedId);
        if (isEdge) {
          e.preventDefault();
          storeDeleteEdge(selectedId);
          setSelectedId(null);
        }
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [selectedId, edges, toggleFade, storeDeleteEdge]);

  const { onPointerDown, onPointerMove, onPointerUp, onWheelNative } = useInteractionControls(
    cameraRef,
    canvasSize,
    pickRequest,
    setSelectedId,
    nodesRef,
    storeMoveNode,
    storeCreateEdge,
    selectedIdRef,
    edgesRef,
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
      const rect = (e.currentTarget as HTMLDivElement).getBoundingClientRect();
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

  const onNearestN = useCallback((ids: Set<string>) => {
    setNearestNIds(ids);
  }, []);

  // Occlusion counts: recomputed only when the camera settles (not per-frame).
  // Map from frontNodeId → N (nodes hidden directly behind it from current viewpoint).
  const [occlusionCounts, setOcclusionCounts] = useState<Map<string, number>>(new Map());

  const onCameraSettle = useCallback(() => {
    const cam = cameraRef.current;
    if (!cam) return;
    const counts = computeOcclusionCounts(nodes, cam, canvasSize);
    setOcclusionCounts(counts);
  }, [nodes, cameraRef, canvasSize]);

  const labelMap = new Map(labelPositions.map((p) => [p.id, p]));

  const toggleGlobalLabels = useCallback((e: React.MouseEvent) => {
    e.stopPropagation();
    setGlobalLabelsHidden((prev) => {
      const next = !prev;
      patchViewerState((v) => {
        v.labelsGlobalHidden = next || undefined;
      });
      scheduleViewSave();
      return next;
    });
  }, []);

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
            hoveredId={hoveredId}
            cameraRef={cameraRef}
            initialCamera3d={viewerState.camera3d}
            onPickRequest={pickRequest}
            onPositions={onPositions}
            onNearestN={onNearestN}
            onCameraSettle={onCameraSettle}
          />
        </Canvas>
      </div>

      {/* Label overlay — real camera projection, updated every frame.
          LOD: show only hovered | selected | nearest-N nodes to avoid forest. */}
      {!globalLabelsHidden && nodes.map((n) => {
        const pos = labelMap.get(n.id);
        if (!pos) return null;
        const isHovered = n.id === hoveredId;
        const isSelected = n.id === selectedId;
        const isNearest = nearestNIds.has(n.id);
        if (!isHovered && !isSelected && !isNearest) return null;
        const pillStyle: React.CSSProperties = {
              background: "rgba(0,0,0,0.55)",
              border: "none",
              borderRadius: 4,
              padding: "3px 6px",
            };
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
              ...pillStyle,
            }}
          >
            <div style={{ whiteSpace: "nowrap" }}>{n.data?.label ?? n.id}</div>
            <div style={{ whiteSpace: "nowrap", fontSize: 9, opacity: 0.6 }}>{n.data?.type}</div>
          </div>
        );
      })}

      {/* Occlusion count badges — "+N" pill at top-right of front node's projected center.
          Only shown when N >= 1. Recomputed on camera settle (not per-frame).
          Full occlusion is allowed — layout never moves (honesty preserved).
          TODO(3d): large-count cap/format deferred */}
      {nodes.map((n) => {
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

      {/* In-node spec-override text — HTML overlay (NOT drei) projected at
          each node's world center. Mirrors the top billboard pattern above. */}
      {nodes.map((n) => {
        const pos = labelMap.get(n.id);
        if (!pos) return null;
        const text = nodeOverrideText(n);
        if (!text) return null;
        const overrideOverlayPill: React.CSSProperties = {
              background: "rgba(0,0,0,0.35)",
              border: "none",
              borderRadius: 4,
              padding: "3px 6px",
            };
        return (
          <div
            key={`override-${n.id}`}
            style={{
              position: "absolute",
              left: pos.cx,
              top: pos.cy,
              transform: "translate(-50%, -50%)",
              fontSize: 9,
              fontFamily: "monospace",
              color: "#e0e0e0",
              pointerEvents: "none",
              lineHeight: 1.25,
              textAlign: "center",
              whiteSpace: "pre",
              zIndex: 12,
              ...overrideOverlayPill,
            }}
          >
            {text}
          </div>
        );
      })}

      {/* Widgets — fixed corner, pointerEvents auto */}
      <HomeButton cameraRef={cameraRef} nodesRef={nodesRef} aspect={canvasSize.w / canvasSize.h} />
      <GlobalLabelsToggle hidden={globalLabelsHidden} onClick={toggleGlobalLabels} />

    </div>
  );
}
