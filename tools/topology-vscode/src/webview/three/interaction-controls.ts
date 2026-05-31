// interaction-controls.ts — useInteractionControls hook and related types.
// Handles CLICK, NODE-DRAG, and SCROLL (dolly/pan) detection.
// Single-pointer empty-space drag (arcball rotation) and dwell→PanPad are removed.

import { useRef, useCallback } from "react";
import * as THREE from "three";
import type { RFNode, NodeData } from "../types";
import { nodeWorldPos, pixelToNDC } from "./geometry-helpers";
import { useThreeStore } from "./store";
import { patchViewerState } from "../state/viewer-state";
import { scheduleSave, scheduleViewSave } from "../save";
import { vscode } from "../vscode-api";
import { postLog } from "../log/post";

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
  phase: "idle" | "pending" | "dragging" | "rotating" | "wiring";
  // Pointer-down snapshot
  downX: number;
  downY: number;
  downTime: number;
  // Previous pointer position (updated during rotating)
  prevX: number;
  prevY: number;
  // Arcball pivot: captured once at gesture start (world space)
  arcballPivot: THREE.Vector3;
  // Two-cylinder gesture-start snapshot (anchored, world-fixed axes)
  arcStartOffset: THREE.Vector3; // cam.position - pivot at gesture start
  arcStartUp: THREE.Vector3;     // cam.up at gesture start
  arcStartQuat: THREE.Quaternion; // camera orientation at gesture start
}

// ---------------------------------------------------------------------------
// PickOptions (shared with scene-content)
// ---------------------------------------------------------------------------

export interface PickOptions {
  excludeId?: string;
  nodesOnly?: boolean;
  ringOnly?: boolean;
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
  selectedIdRef: React.MutableRefObject<string | null>,
) {
  const state = useRef<ControlState>({
    phase: "idle",
    downX: 0, downY: 0, downTime: 0,
    prevX: 0, prevY: 0,
    arcballPivot: new THREE.Vector3(0, 0, 0),
    arcStartOffset: new THREE.Vector3(),
    arcStartUp: new THREE.Vector3(0, 1, 0),
    arcStartQuat: new THREE.Quaternion(),
  });

  // Node-drag state: set when pointer-down lands on a node.
  const nodeDragRef = useRef<{
    nodeId: string;
    planePointAtStart: THREE.Vector3;
    nodeCenterAtStart: THREE.Vector3;
    rfPosAtStart: { x: number; y: number };
  } | null>(null);

  // Wiring state: set when pointer-down lands on the torus ring of the selected node.
  const wiringRef = useRef<{ sourceId: string } | null>(null);

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

      // Clear previous drag/wiring state.
      nodeDragRef.current = null;
      wiringRef.current = null;

      // Pick node under cursor.
      const rect = (e.currentTarget as HTMLDivElement).getBoundingClientRect();
      const { ndcX, ndcY } = pixelToNDC(e.clientX, e.clientY, rect);
      const hitId = pickRequest.current?.(ndcX, ndcY) ?? null;

      // Check for ring hit on the selected node → start wiring.
      const ringHit = pickRequest.current?.(ndcX, ndcY, { ringOnly: true }) ?? null;
      postLog("wire-ringpick", { ringHit: ringHit ?? undefined, selectedId: selectedIdRef.current ?? undefined, hitId: hitId ?? undefined });
      if (ringHit !== null) {
        wiringRef.current = { sourceId: ringHit };
        s.phase = "pending";
        (e.currentTarget as HTMLDivElement).setPointerCapture(e.pointerId);
        return;
      }

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
      } else {
        // Empty-space pointerdown: capture arcball pivot once.
        const selId = selectedIdRef.current;
        const selNode = selId ? nodesRef.current.find((n) => n.id === selId) : null;
        if (selNode) {
          s.arcballPivot = nodeWorldPos(selNode);
        } else {
          // No selection: pivot on the point at screen center projected to the z=0 plane,
          // so the view stays framed instead of swinging around an off-screen world origin.
          const cx = rect.left + rect.width / 2;
          const cy = rect.top + rect.height / 2;
          const centerOnPlane = unprojectToPlane(cx, cy, rect);
          s.arcballPivot = centerOnPlane ? centerOnPlane.clone() : new THREE.Vector3(0, 0, 0);
        }
        // Snapshot camera state for the anchored two-cylinder rotation.
        const cam0 = cameraRef.current;
        if (cam0) {
          cam0.updateMatrixWorld(true);
          s.arcStartOffset = cam0.position.clone().sub(s.arcballPivot);
          s.arcStartUp = cam0.up.clone();
          s.arcStartQuat = cam0.quaternion.clone();
        }
      }

      (e.currentTarget as HTMLDivElement).setPointerCapture(e.pointerId);
    },
    [cameraRef, nodesRef, pickRequest, unprojectToPlane],
  );

  const onPointerMove = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      const s = state.current;
      if (s.phase === "idle") return;

      const dx = e.clientX - s.downX;
      const dy = e.clientY - s.downY;
      const dist = Math.sqrt(dx * dx + dy * dy);

      if (s.phase === "pending" && dist > MOVE_SLOP_PX) {
        if (wiringRef.current) {
          s.phase = "wiring";
        } else if (nodeDragRef.current) {
          s.phase = "dragging";
        } else {
          // Empty-space drag: start arcball rotation.
          s.phase = "rotating";
        }
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

      if (s.phase === "rotating") {
        const cam = cameraRef.current;
        if (cam) {
          const pivot = s.arcballPivot;
          const ROT_SPEED = 0.005;
          // Two cylinders: both rotation axes lie in the node's plane (z=0) through
          // the pivot. Horizontal drag rotates about world Y, vertical drag about
          // world X. Decoupled, world-fixed axes; anchored to total drag from the
          // down-point so a closed loop returns to start (no accumulation).
          const angY = 1 * (e.clientX - s.downX) * ROT_SPEED; // horizontal -> world Y
          const angX = 1 * (e.clientY - s.downY) * ROT_SPEED; // vertical   -> world X
          const qx = new THREE.Quaternion().setFromAxisAngle(new THREE.Vector3(1, 0, 0), angX);
          const qy = new THREE.Quaternion().setFromAxisAngle(new THREE.Vector3(0, 1, 0), angY);
          const rot = qy.clone().multiply(qx);
          const rotInv = rot.clone().invert();
          cam.position.copy(pivot).add(s.arcStartOffset.clone().applyQuaternion(rotInv));
          cam.quaternion.copy(rotInv).multiply(s.arcStartQuat);
          cam.up.copy(s.arcStartUp.clone().applyQuaternion(rotInv));
          cam.updateMatrixWorld(true);
          commitCamera(cam);
        }
      }
    },
    [cameraRef, nodesRef, onMoveNode, scheduleNodeMove, unprojectToPlane],
  );

  const onPointerUp = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      const s = state.current;

      // Wiring completed: if dropped on a node, create an edge.
      if (s.phase === "wiring" || (s.phase === "pending" && wiringRef.current !== null)) {
        if (s.phase === "wiring" && wiringRef.current) {
          const rect = (e.currentTarget as HTMLDivElement).getBoundingClientRect();
          const { ndcX, ndcY } = pixelToNDC(e.clientX, e.clientY, rect);
          const targetId = pickRequest.current?.(ndcX, ndcY, { excludeId: wiringRef.current.sourceId, nodesOnly: true }) ?? null;
          if (targetId !== null && targetId !== wiringRef.current.sourceId) {
            storeCreateEdge(wiringRef.current.sourceId, null, targetId, null);
          }
        }
        // pending+wiring (click on ring without drag): fall through to normal click/select path.
        wiringRef.current = null;
        if (s.phase === "wiring") {
          s.phase = "idle";
          (e.currentTarget as HTMLDivElement).releasePointerCapture(e.pointerId);
          return;
        }
      }

      // Node drag completed: persist position.
      if (s.phase === "dragging" && nodeDragRef.current) {
        const nd = nodeDragRef.current;
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

      // Arcball rotation completed: reset state without triggering select.
      if (s.phase === "rotating") {
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
        // Pinch-to-zoom toward the cursor: multiplicatively scale the camera's
        // distance to the world point under the cursor on the z=0 plane, keeping
        // that point fixed on screen. Exponential (uniform per step). >1 deltaY
        // (pinch out) zooms out. Works at any tilt and stays consistent with the
        // arcball, which also anchors on a plane point.
        const ZOOM_BASE = 1.01;
        let factor = Math.pow(ZOOM_BASE, e.deltaY);
        const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
        const target = unprojectToPlane(e.clientX, e.clientY, rect);
        if (target) {
          const minHeight = 5; // never cross/touch the plane
          if (cam.position.z * factor < minHeight) factor = minHeight / cam.position.z;
          cam.position.sub(target).multiplyScalar(factor).add(target);
        }
      } else {
        // Two-finger scroll pans along the z=0 plane: translate the camera by the
        // world-space vector between the plane point under the cursor and the point
        // under cursor+scroll-delta. Stays in the plane, tilt-aware (equals the old
        // direct x/y pan when square-on).
        const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
        const a = unprojectToPlane(e.clientX, e.clientY, rect);
        const b = unprojectToPlane(e.clientX + e.deltaX, e.clientY + e.deltaY, rect);
        if (a && b) {
          cam.position.add(b.sub(a));
        }
      }
      // Commit camera position after each wheel step (scheduleViewSave debounces).
      commitCamera(cam);
    },
    [cameraRef, nodesRef, unprojectToPlane],
  );

  return { onPointerDown, onPointerMove, onPointerUp, onWheelNative };
}
