// interaction-controls.ts — useInteractionControls hook and related types.
// Handles CLICK, NODE-DRAG, and SCROLL (dolly/pan) detection.
// Single-pointer empty-space drag (arcball rotation) and dwell→PanPad are removed.

import { useRef, useCallback } from "react";
import * as THREE from "three";
import type { RFNode, NodeData } from "../types";
import { nodeWorldPos, pixelToNDC, worldPerPixel } from "./geometry-helpers";
import { useThreeStore } from "./store";
import { patchViewerState } from "../state/viewer-state";
import { scheduleSave, scheduleViewSave } from "../save";
import { vscode } from "../vscode-api";

// ---------------------------------------------------------------------------
// Camera persistence helper
// ---------------------------------------------------------------------------

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
// Gesture discrimination constants
// ---------------------------------------------------------------------------

/** Max pointer-down → up duration (ms) to count as a CLICK. */
const CLICK_MAX_MS = 150;
/** Pixel movement threshold between CLICK and DRAG. */
const MOVE_SLOP_PX = 6;

// ---------------------------------------------------------------------------
// ControlState
// ---------------------------------------------------------------------------

export interface ControlState {
  // Interaction phase
  phase: "idle" | "pending" | "dragging";
  // Pointer-down snapshot
  downX: number;
  downY: number;
  downTime: number;
  // Previous pointer position (unused after arcball removal; kept for future use)
  prevX: number;
  prevY: number;
}

// ---------------------------------------------------------------------------
// PickOptions (shared with scene-content)
// ---------------------------------------------------------------------------

export interface PickOptions {
  excludeId?: string;
  nodesOnly?: boolean;
}

// ---------------------------------------------------------------------------
// useInteractionControls
// ---------------------------------------------------------------------------

export function useInteractionControls(
  cameraRef: React.MutableRefObject<THREE.PerspectiveCamera | null>,
  canvasSize: { w: number; h: number },
  pickRequest: React.MutableRefObject<((ndcX: number, ndcY: number, opts?: PickOptions) => string | null) | null>,
  onSelect: (id: string | null) => void,
  nodesRef: React.MutableRefObject<RFNode<NodeData>[]>,
  onMoveNode: (id: string, x: number, y: number) => void,
  storeCreateEdge: (sourceId: string, sourceHandle: string | null, targetId: string, targetHandle: string | null) => void,
) {
  const state = useRef<ControlState>({
    phase: "idle",
    downX: 0, downY: 0, downTime: 0,
    prevX: 0, prevY: 0,
  });

  // Node-drag state: set when pointer-down lands on a node.
  const nodeDragRef = useRef<{
    nodeId: string;
    planePointAtStart: THREE.Vector3;
    nodeCenterAtStart: THREE.Vector3;
    rfPosAtStart: { x: number; y: number };
  } | null>(null);

  // Throttle node-move IPC: one message per animation frame during drag.
  const pendingNodeMove = useRef<{ nodeId: string; x: number; y: number } | null>(null);
  const rafPending = useRef(false);

  const flushNodeMove = useCallback((nodeId: string, x: number, y: number) => {
    vscode.postMessage({ type: "node-move", nodeId, x, y, z: 0 });
  }, []);

  const scheduleNodeMove = useCallback((nodeId: string, x: number, y: number) => {
    pendingNodeMove.current = { nodeId, x, y };
    if (!rafPending.current) {
      rafPending.current = true;
      requestAnimationFrame(() => {
        rafPending.current = false;
        const p = pendingNodeMove.current;
        if (p) {
          flushNodeMove(p.nodeId, p.x, p.y);
          pendingNodeMove.current = null;
        }
      });
    }
  }, [flushNodeMove]);

  // ------ helpers ------

  /** Unproject a pointer position (client coords) to the z=0 world plane. */
  const unprojectToPlane = useCallback(
    (clientX: number, clientY: number, rect: DOMRect): THREE.Vector3 | null => {
      const cam = cameraRef.current;
      if (!cam) return null;
      const { ndcX, ndcY } = pixelToNDC(clientX, clientY, rect);
      const raycaster = new THREE.Raycaster();
      raycaster.setFromCamera(new THREE.Vector2(ndcX, ndcY), cam);
      const plane = new THREE.Plane(new THREE.Vector3(0, 0, 1), 0);
      const target = new THREE.Vector3();
      const hit = raycaster.ray.intersectPlane(plane, target);
      return hit ? target : null;
    },
    [cameraRef],
  );

  // ------ pointer event handlers ------

  const onPointerDown = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      const s = state.current;
      s.downX = e.clientX;
      s.downY = e.clientY;
      s.downTime = Date.now();
      s.prevX = e.clientX;
      s.prevY = e.clientY;
      s.phase = "pending";

      // Clear previous node-drag state.
      nodeDragRef.current = null;

      // Pick node under cursor.
      const rect = (e.currentTarget as HTMLDivElement).getBoundingClientRect();
      const { ndcX, ndcY } = pixelToNDC(e.clientX, e.clientY, rect);
      const hitId = pickRequest.current?.(ndcX, ndcY) ?? null;

      if (hitId !== null) {
        // Node hit: record drag origin for node-drag phase.
        const planePoint = unprojectToPlane(e.clientX, e.clientY, rect);
        const node = nodesRef.current.find((n) => n.id === hitId);
        if (planePoint && node) {
          nodeDragRef.current = {
            nodeId: hitId,
            planePointAtStart: planePoint.clone(),
            nodeCenterAtStart: nodeWorldPos(node),
            rfPosAtStart: { x: node.position.x, y: node.position.y },
          };
        }
      }
      // Empty-space pointerdown: no rotation, no dwell timer — just capture.

      (e.currentTarget as HTMLDivElement).setPointerCapture(e.pointerId);
    },
    [nodesRef, pickRequest, unprojectToPlane],
  );

  const onPointerMove = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      const s = state.current;
      if (s.phase === "idle") return;

      const dx = e.clientX - s.downX;
      const dy = e.clientY - s.downY;
      const dist = Math.sqrt(dx * dx + dy * dy);

      if (s.phase === "pending" && dist > MOVE_SLOP_PX) {
        // Transition to drag only when a node was hit; empty-space move does nothing.
        if (nodeDragRef.current) {
          s.phase = "dragging";
        }
        // Empty-space drag: stay in "pending" — no arcball.
      }

      if (s.phase === "dragging" && nodeDragRef.current) {
        // Node drag: move node on the z=0 plane.
        const nd = nodeDragRef.current;
        const rect = (e.currentTarget as HTMLDivElement).getBoundingClientRect();
        const planePoint = unprojectToPlane(e.clientX, e.clientY, rect);
        if (planePoint) {
          const node = nodesRef.current.find((n) => n.id === nd.nodeId);
          if (node) {
            const w = node.data?.width ?? 110;
            const h = node.data?.height ?? 60;
            const newCenterX = planePoint.x + (nd.nodeCenterAtStart.x - nd.planePointAtStart.x);
            const newCenterY = planePoint.y + (nd.nodeCenterAtStart.y - nd.planePointAtStart.y);
            const newPosX = newCenterX - w / 2;
            const newPosY = -newCenterY - h / 2;
            onMoveNode(nd.nodeId, newPosX, newPosY);
            scheduleNodeMove(nd.nodeId, newPosX, newPosY);
          }
        }
        s.prevX = e.clientX;
        s.prevY = e.clientY;
      }
    },
    [nodesRef, onMoveNode, scheduleNodeMove, unprojectToPlane],
  );

  const onPointerUp = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      const s = state.current;

      // Node drag completed: check for drag-to-wire, else persist position.
      if (s.phase === "dragging" && nodeDragRef.current) {
        const nd = nodeDragRef.current;
        const rect = (e.currentTarget as HTMLDivElement).getBoundingClientRect();
        const { ndcX, ndcY } = pixelToNDC(e.clientX, e.clientY, rect);
        const sourceId = nd.nodeId;
        const targetId = pickRequest.current?.(ndcX, ndcY, { excludeId: sourceId, nodesOnly: true }) ?? null;
        if (targetId !== null && targetId !== sourceId) {
          // WIRE: revert the source node to its start position, then create the edge.
          onMoveNode(sourceId, nd.rfPosAtStart.x, nd.rfPosAtStart.y);
          storeCreateEdge(sourceId, null, targetId, null);
          nodeDragRef.current = null;
          s.phase = "idle";
          return;
        }
        // Normal move: persist position.
        const node = useThreeStore.getState().nodes.find((n) => n.id === nd.nodeId);
        if (node) {
          patchViewerState((v) => {
            if (!v.nodes) v.nodes = {};
            const existing = v.nodes[node.id];
            v.nodes[node.id] = { ...(existing ?? {}), x: node.position.x, y: node.position.y };
          });
          scheduleViewSave();
          scheduleSave();
          pendingNodeMove.current = null;
          rafPending.current = false;
          flushNodeMove(node.id, node.position.x, node.position.y);
        }
        nodeDragRef.current = null;
        s.phase = "idle";
        return;
      }

      nodeDragRef.current = null;

      if (s.phase === "pending") {
        const elapsed = Date.now() - s.downTime;
        const ddx = e.clientX - s.downX;
        const ddy = e.clientY - s.downY;
        const clickDist = Math.sqrt(ddx * ddx + ddy * ddy);
        if (elapsed < CLICK_MAX_MS && clickDist < MOVE_SLOP_PX) {
          // CLICK → pick (selects node or deselects on empty space)
          const rect = (e.currentTarget as HTMLDivElement).getBoundingClientRect();
          const { ndcX, ndcY } = pixelToNDC(e.clientX, e.clientY, rect);
          const hitId = pickRequest.current?.(ndcX, ndcY) ?? null;
          onSelect(hitId);
        }
      }

      s.phase = "idle";
    },
    [flushNodeMove, onMoveNode, onSelect, pickRequest, storeCreateEdge],
  );

  // Exposed so ThreeView can attach a non-passive native listener.
  const onWheelNative = useCallback(
    (e: WheelEvent) => {
      const cam = cameraRef.current;
      if (!cam) return;
      // Prevent browser scroll / back-nav gestures (requires non-passive listener).
      e.preventDefault();

      if (e.ctrlKey) {
        // Pinch-to-zoom: multiplicative dolly on height above the z=0 plane.
        // Exponential so a step feels uniform at every scale (industry standard).
        // Base is the single speed knob; >1 deltaY (pinch out) zooms out.
        const ZOOM_BASE = 1.01;
        const factor = Math.pow(ZOOM_BASE, e.deltaY);
        const minHeight = 5; // never cross/touch the plane
        cam.position.z = Math.max(minHeight, cam.position.z * factor);
      } else {
        // Camera is locked square-on (looking straight down -z toward the z=0 plane,
        // up = +y). Pan directly in world x/y — no matrix-column extraction needed.
        const wpp = worldPerPixel(cam, canvasSize.h);
        cam.position.x += e.deltaX * wpp;
        cam.position.y -= e.deltaY * wpp;
      }
      // Commit camera position after each wheel step (scheduleViewSave debounces).
      commitCamera(cam);
    },
    [cameraRef, canvasSize.h, nodesRef],
  );

  return { onPointerDown, onPointerMove, onPointerUp, onWheelNative };
}
