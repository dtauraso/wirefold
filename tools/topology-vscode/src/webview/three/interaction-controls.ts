// interaction-controls.ts — useInteractionControls hook and related types.
// Handles CLICK, DRAG (arcball), SCROLL (dolly), and DWELL detection.

import { useRef, useCallback } from "react";
import * as THREE from "three";
import type { RFNode, NodeData } from "../types";
import { nodeWorldPos, sceneCenter, worldPerPixel, pixelToNDC } from "./geometry-helpers";
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
/** Stationary dwell on empty canvas before the pan pad appears (ms, mouse). */
const DWELL_MS = 200;
/** Pixel movement threshold between CLICK and DRAG. */
const MOVE_SLOP_PX = 6;

// ---------------------------------------------------------------------------
// ControlState
// ---------------------------------------------------------------------------

export interface ControlState {
  // Interaction phase
  phase: "idle" | "pending" | "dragging" | "dwell-wait" | "panning";
  // Pointer-down snapshot
  downX: number;
  downY: number;
  downTime: number;
  // Previous pointer position (for incremental arcball)
  prevX: number;
  prevY: number;
  // Dwell timer id
  dwellTimer: ReturnType<typeof setTimeout> | null;
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
  onPanPadActive: (pos: { x: number; y: number } | null) => void,
  nodesRef: React.MutableRefObject<RFNode<NodeData>[]>,
  onMoveNode: (id: string, x: number, y: number) => void,
  storeCreateEdge: (sourceId: string, sourceHandle: string | null, targetId: string, targetHandle: string | null) => void,
) {
  const state = useRef<ControlState>({
    phase: "idle",
    downX: 0, downY: 0, downTime: 0,
    prevX: 0, prevY: 0,
    dwellTimer: null,
  });

  // Node-drag state: set when pointer-down lands on a node.
  const nodeDragRef = useRef<{
    nodeId: string;
    planePointAtStart: THREE.Vector3;
    nodeCenterAtStart: THREE.Vector3;
    rfPosAtStart: { x: number; y: number };
  } | null>(null);

  // Throttle node-move IPC: one message per animation frame during drag.
  // rafPending is truthy when a rAF is already scheduled; the scheduled
  // callback reads the latest position from pendingNodeMove.
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

  // Pivot for the current drag (world space)
  const dragPivot = useRef(new THREE.Vector3());
  // Pan-pad origin for panning
  const panPadOrigin = useRef({ x: 0, y: 0 });
  // Camera state at pan-pad activation
  const panStartCamPos = useRef(new THREE.Vector3());

  // ------ helpers ------

  /** Pick the 3D point under the cursor (raycasts; falls back to scene center). */
  const pickPivot = useCallback(
    (clientX: number, clientY: number, rect: DOMRect): THREE.Vector3 => {
      const cam = cameraRef.current;
      if (!cam) return new THREE.Vector3();
      const { ndcX, ndcY } = pixelToNDC(clientX, clientY, rect);
      const hitId = pickRequest.current?.(ndcX, ndcY) ?? null;
      if (hitId !== null) {
        // Raycast a node: return its world position as pivot.
        // We do a simple plane intersection at z=0 as a close-enough pivot.
        const raycaster = new THREE.Raycaster();
        raycaster.setFromCamera(new THREE.Vector2(ndcX, ndcY), cam);
        const plane = new THREE.Plane(new THREE.Vector3(0, 0, 1), 0);
        const target = new THREE.Vector3();
        raycaster.ray.intersectPlane(plane, target);
        return target.lengthSq() > 0 ? target : new THREE.Vector3();
      }
      // Empty space: use scene center (z=0 plane intersection)
      const raycaster = new THREE.Raycaster();
      raycaster.setFromCamera(new THREE.Vector2(ndcX, ndcY), cam);
      const plane = new THREE.Plane(new THREE.Vector3(0, 0, 1), 0);
      const target = new THREE.Vector3();
      raycaster.ray.intersectPlane(plane, target);
      return target.lengthSq() > 0 ? target : new THREE.Vector3();
    },
    [cameraRef, pickRequest],
  );

  /**
   * Arcball rotation: given the drag vector (dx, dy in pixels), derive a
   * quaternion that rotates the camera around `pivot` by an angle proportional
   * to drag arc length. The rotation axis is perpendicular to the drag tangent
   * and lies in the screen plane → mapped to world space via the camera.
   */
  const applyArcball = useCallback(
    (dx: number, dy: number, pivot: THREE.Vector3) => {
      const cam = cameraRef.current;
      if (!cam) return;
      const arcLen = Math.sqrt(dx * dx + dy * dy);
      if (arcLen < 0.5) return;

      // Screen-plane rotation axis: perpendicular to drag tangent.
      // drag tangent in NDC-like space: (dx, -dy) (y flipped for screen).
      // Perpendicular: rotate 90° CCW → (dy, dx) → normalize.
      // This axis is in camera's local screen plane.
      const axisCam = new THREE.Vector3(dy / arcLen, dx / arcLen, 0).normalize();

      // Map to world space.
      const axisWorld = axisCam.clone().applyQuaternion(cam.quaternion);

      // Angle proportional to drag distance (pixels → radians).
      const angle = (arcLen / Math.max(canvasSize.w, canvasSize.h)) * Math.PI;

      const q = new THREE.Quaternion().setFromAxisAngle(axisWorld, angle);

      // Orbit camera around pivot.
      const offset = cam.position.clone().sub(pivot);
      offset.applyQuaternion(q);
      cam.position.copy(pivot).add(offset);
      cam.quaternion.premultiply(q);
    },
    [cameraRef, canvasSize],
  );

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

      // Clear any existing dwell timer.
      if (s.dwellTimer !== null) { clearTimeout(s.dwellTimer); s.dwellTimer = null; }

      // Clear previous node-drag state.
      nodeDragRef.current = null;

      // Pick node under cursor.
      const rect = (e.currentTarget as HTMLDivElement).getBoundingClientRect();
      const { ndcX, ndcY } = pixelToNDC(e.clientX, e.clientY, rect);
      const hitId = pickRequest.current?.(ndcX, ndcY) ?? null;

      if (hitId !== null) {
        // Node hit: record drag origin for node-drag phase.
        // Unproject to z=0 to get the world-plane point under the pointer.
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
        // Do NOT start dwell timer when a node is hit.
      } else {
        // Empty canvas: start dwell timer for pan pad.
        s.dwellTimer = setTimeout(() => {
          if (state.current.phase === "pending") {
            state.current.phase = "panning";
            panPadOrigin.current = { x: e.clientX, y: e.clientY };
            panStartCamPos.current = cameraRef.current?.position.clone() ?? new THREE.Vector3();
            onPanPadActive({ x: e.clientX, y: e.clientY });
          }
          state.current.dwellTimer = null;
        }, DWELL_MS);
      }

      (e.currentTarget as HTMLDivElement).setPointerCapture(e.pointerId);
    },
    [cameraRef, nodesRef, onPanPadActive, pickRequest, unprojectToPlane],
  );

  const onPointerMove = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      const s = state.current;
      if (s.phase === "idle") return;

      const dx = e.clientX - s.downX;
      const dy = e.clientY - s.downY;
      const dist = Math.sqrt(dx * dx + dy * dy);

      if (s.phase === "pending" && dist > MOVE_SLOP_PX) {
        // Cancel dwell; transition to drag.
        if (s.dwellTimer !== null) { clearTimeout(s.dwellTimer); s.dwellTimer = null; }
        onPanPadActive(null);
        s.phase = "dragging";

        if (nodeDragRef.current) {
          // Do NOT compute arcball pivot — node drag suppresses arcball.
        } else {
          // Empty-space drag: compute arcball pivot.
          const rect = (e.currentTarget as HTMLDivElement).getBoundingClientRect();
          dragPivot.current = pickPivot(s.downX, s.downY, rect);
        }
      }

      if (s.phase === "dragging") {
        if (nodeDragRef.current) {
          // Node drag: move node on the z=0 plane.
          const nd = nodeDragRef.current;
          const rect = (e.currentTarget as HTMLDivElement).getBoundingClientRect();
          const planePoint = unprojectToPlane(e.clientX, e.clientY, rect);
          if (planePoint) {
            const node = nodesRef.current.find((n) => n.id === nd.nodeId);
            if (node) {
              const w = node.data?.width ?? 110;
              const h = node.data?.height ?? 60;
              // newCenter = planePoint + (nodeCenterAtStart - planePointAtStart)
              const newCenterX = planePoint.x + (nd.nodeCenterAtStart.x - nd.planePointAtStart.x);
              const newCenterY = planePoint.y + (nd.nodeCenterAtStart.y - nd.planePointAtStart.y);
              // Invert nodeWorldPos: worldX = pos.x + w/2, worldY = -(pos.y + h/2)
              // → pos.x = worldX - w/2, pos.y = -worldY - h/2
              const newPosX = newCenterX - w / 2;
              const newPosY = -newCenterY - h / 2;
              onMoveNode(nd.nodeId, newPosX, newPosY);
              scheduleNodeMove(nd.nodeId, newPosX, newPosY);
            }
          }
        } else {
          // Arcball rotate (empty-space drag).
          const ddx = e.clientX - s.prevX;
          const ddy = e.clientY - s.prevY;
          applyArcball(ddx, ddy, dragPivot.current);
        }
        s.prevX = e.clientX;
        s.prevY = e.clientY;
      }

      if (s.phase === "panning") {
        const cam = cameraRef.current;
        if (!cam) return;
        // Pan camera in its local XY plane proportional to pointer delta.
        const panDx = (e.clientX - panPadOrigin.current.x);
        const panDy = (e.clientY - panPadOrigin.current.y);
        // worldPerPixel uses true perpendicular distance — correct after rotation.
        const wpp = worldPerPixel(cam, canvasSize.h);
        const rightDir = new THREE.Vector3().setFromMatrixColumn(cam.matrixWorld, 0);
        const upDir = new THREE.Vector3().setFromMatrixColumn(cam.matrixWorld, 1);
        cam.position.copy(panStartCamPos.current)
          .addScaledVector(rightDir, -panDx * wpp)
          .addScaledVector(upDir, panDy * wpp);
      }
    },
    [applyArcball, cameraRef, canvasSize, nodesRef, onMoveNode, onPanPadActive, pickPivot, scheduleNodeMove, unprojectToPlane],
  );

  const onPointerUp = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      const s = state.current;
      if (s.dwellTimer !== null) { clearTimeout(s.dwellTimer); s.dwellTimer = null; }
      onPanPadActive(null);

      // Node drag completed: check for drag-to-wire, else persist position.
      if (s.phase === "dragging" && nodeDragRef.current) {
        const nd = nodeDragRef.current;
        // Hit-test at release point excluding the source node, nodes only.
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
        // Read from store directly so we get the position applied by the last onPointerMove,
        // which may not have re-rendered yet (nodesRef.current could be one frame stale).
        const node = useThreeStore.getState().nodes.find((n) => n.id === nd.nodeId);
        if (node) {
          patchViewerState((v) => {
            if (!v.nodes) v.nodes = {};
            const existing = v.nodes[node.id];
            v.nodes[node.id] = { ...(existing ?? {}), x: node.position.x, y: node.position.y };
          });
          scheduleViewSave();
          scheduleSave();
          // Final flush: cancel any pending rAF and send the precise final position.
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
          // CLICK → pick
          const rect = (e.currentTarget as HTMLDivElement).getBoundingClientRect();
          const { ndcX, ndcY } = pixelToNDC(e.clientX, e.clientY, rect);
          const hitId = pickRequest.current?.(ndcX, ndcY) ?? null;
          onSelect(hitId);
        }
      }

      // Commit camera state after orbit (empty-space drag) or pan gesture ends.
      // Orbit reuses phase "dragging" with no nodeDragRef; pan uses "panning".
      if ((s.phase === "panning" || (s.phase === "dragging" && !nodeDragRef.current)) && cameraRef.current) {
        commitCamera(cameraRef.current);
      }

      s.phase = "idle";
    },
    [cameraRef, flushNodeMove, nodesRef, onMoveNode, onPanPadActive, onSelect, pickRequest, storeCreateEdge],
  );

  const onWheel = useCallback(
    (e: React.WheelEvent<HTMLDivElement>) => {
      const cam = cameraRef.current;
      if (!cam) return;
      // Dolly along view direction. Positive deltaY = scroll-down = zoom out.
      const dir = new THREE.Vector3(0, 0, 1).applyQuaternion(cam.quaternion);
      // Use distance from camera to scene center (correct after rotation).
      const center = sceneCenter(nodesRef.current);
      const dist = cam.position.distanceTo(center);
      const speed = 0.001 * Math.max(dist, 10);
      cam.position.addScaledVector(dir, e.deltaY * speed);
      // Commit camera position after each wheel step (scheduleViewSave debounces).
      commitCamera(cam);
    },
    [cameraRef, nodesRef],
  );

  return { onPointerDown, onPointerMove, onPointerUp, onWheel };
}
