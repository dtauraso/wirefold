// interaction-controls.ts — useInteractionControls hook and related types.
// Handles CLICK, NODE-DRAG, and SCROLL (dolly/pan) detection.
// Single-pointer empty-space drag (arcball rotation) and dwell→PanPad are removed.

import { useRef, useCallback } from "react";
import * as THREE from "three";
import type { RFNode, RFEdge, NodeData, EdgeData } from "../types";
import type { MoveEntry } from "../../messages";
import { nodeWorldPos, pixelToNDC, pointerRingAnchor } from "./geometry-helpers";
import { NODE_DIM_FALLBACK } from "../state/node-dims";
import { useThreeStore } from "./store";
import { patchViewerState } from "../state/viewer-state";
import { scheduleViewSave } from "../save";
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
  phase: "idle" | "pending" | "dragging" | "rotating" | "wiring" | "port-move";
  // Pointer-down snapshot
  downX: number;
  downY: number;
  downTime: number;
  // Previous pointer position (updated during rotating)
  prevX: number;
  prevY: number;
  // True when the pointer-down hit empty space (not a node or edge); gates arcball rotation.
  emptyDown: boolean;
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
  portOnly?: boolean;
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
  edgesRef: React.MutableRefObject<RFEdge<EdgeData>[]>,
) {
  const state = useRef<ControlState>({
    phase: "idle",
    downX: 0, downY: 0, downTime: 0,
    prevX: 0, prevY: 0,
    emptyDown: false,
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

  // Wiring state: set when pointer-down lands on an UNCONNECTED port sphere
  // (drag port→port creates an edge).
  const wiringRef = useRef<{ nodeId: string; portName: string; isInput: boolean } | null>(null);

  // Port-move state: set when pointer-down lands on a CONNECTED port sphere
  // (drag slides the port along its node's ring; the incident edge follows).
  const portMoveRef = useRef<{
    nodeId: string;
    portName: string;
    isInput: boolean;
    nodeCenter: THREE.Vector3;
  } | null>(null);

  // Throttle node-move IPC: one message per animation frame during drag.
  const pendingNodeMove = useRef<{ nodeId: string; x: number; y: number } | null>(null);
  const rafPending = useRef(false);

  const flushNodeMove = useCallback((nodeId: string, x: number, y: number) => {
    // Decentralized node-move: mail-sort the move to the moved node + every incident
    // edge (source===moved || target===moved). TS owns the graph and computes the
    // incident edges; Go's per-node/per-edge goroutines own the recompute. Every
    // entry carries the same moved node id + new position; keys are node id + edge ids.
    const entry: MoveEntry = { nodeId, x, y, z: 0 };
    const entries: Record<string, MoveEntry> = { [nodeId]: entry };
    for (const e of edgesRef.current) {
      if (e.source === nodeId || e.target === nodeId) {
        entries[e.id] = entry;
      }
    }
    vscode.postMessage({ type: "edit", op: "update", entries });
  }, [edgesRef]);

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

  // Edge ids incident on a specific port (output → source/sourceHandle, input →
  // target/targetHandle). Used both to decide connected-vs-unconnected and to build
  // the port-anchor `keys` fan-out.
  const incidentEdgeIds = useCallback(
    (nodeId: string, portName: string, isInput: boolean): string[] => {
      const ids: string[] = [];
      for (const e of edgesRef.current) {
        const hit = isInput
          ? e.target === nodeId && (e.targetHandle ?? null) === portName
          : e.source === nodeId && (e.sourceHandle ?? null) === portName;
        if (hit) ids.push(e.id);
      }
      return ids;
    },
    [edgesRef],
  );

  // Throttle port-anchor IPC: one message per animation frame during a port drag.
  const pendingAnchor = useRef<{
    nodeId: string;
    portName: string;
    isInput: boolean;
    anchor: { x: number; y: number; z: number };
  } | null>(null);
  const anchorRafPending = useRef(false);

  const flushPortAnchor = useCallback(
    (p: { nodeId: string; portName: string; isInput: boolean; anchor: { x: number; y: number; z: number } }) => {
      // Decentralized port-anchor: mail-sort to the owning node + each edge incident
      // on this port. Go's per-node/per-edge goroutines own the recompute + re-emit.
      const keys = [p.nodeId, ...incidentEdgeIds(p.nodeId, p.portName, p.isInput)];
      vscode.postMessage({
        type: "edit",
        op: "port-anchor",
        node: p.nodeId,
        port: p.portName,
        isInput: p.isInput,
        anchor: p.anchor,
        keys,
      });
    },
    [incidentEdgeIds],
  );

  const schedulePortAnchor = useCallback(
    (p: { nodeId: string; portName: string; isInput: boolean; anchor: { x: number; y: number; z: number } }) => {
      pendingAnchor.current = p;
      if (!anchorRafPending.current) {
        anchorRafPending.current = true;
        requestAnimationFrame(() => {
          anchorRafPending.current = false;
          const q = pendingAnchor.current;
          if (q) {
            flushPortAnchor(q);
            pendingAnchor.current = null;
          }
        });
      }
    },
    [flushPortAnchor],
  );

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

  // ------ helpers ------

  /** Parse a portId of the form `nodeId:in:portName` or `nodeId:out:portName`. */
  function parsePortId(pid: string): { nodeId: string; isInput: boolean; portName: string } {
    const i = pid.indexOf(":");
    const nodeId = pid.slice(0, i);
    const rest = pid.slice(i + 1);
    const j = rest.indexOf(":");
    const dir = rest.slice(0, j);
    const portName = rest.slice(j + 1);
    return { nodeId, isInput: dir === "in", portName };
  }

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
      s.emptyDown = false;

      // Clear previous drag/wiring state.
      nodeDragRef.current = null;
      wiringRef.current = null;
      portMoveRef.current = null;

      // Pick node under cursor.
      const rect = (e.currentTarget as HTMLDivElement).getBoundingClientRect();
      const { ndcX, ndcY } = pixelToNDC(e.clientX, e.clientY, rect);

      // Check for port hit. A CONNECTED port (has an incident edge) drags to MOVE the
      // port along its node's ring; an UNCONNECTED port drags port→port to WIRE a new
      // edge. Both resolve from a "pending" phase on first move past the slop.
      const portHit = pickRequest.current?.(ndcX, ndcY, { portOnly: true }) ?? null;
      if (portHit !== null) {
        const p = parsePortId(portHit);
        const connected = incidentEdgeIds(p.nodeId, p.portName, p.isInput).length > 0;
        if (connected) {
          const node = nodesRef.current.find((n) => n.id === p.nodeId);
          if (node) {
            portMoveRef.current = {
              nodeId: p.nodeId,
              portName: p.portName,
              isInput: p.isInput,
              nodeCenter: nodeWorldPos(node),
            };
          }
        } else {
          wiringRef.current = p;
        }
        s.phase = "pending";
        (e.currentTarget as HTMLDivElement).setPointerCapture(e.pointerId);
        return;
      }

      const hitId = pickRequest.current?.(ndcX, ndcY) ?? null;
      s.emptyDown = (hitId === null);

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
    [cameraRef, nodesRef, pickRequest, unprojectToPlane, incidentEdgeIds],
  );

  const onPointerMove = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      const s = state.current;
      if (s.phase === "idle") return;

      const dx = e.clientX - s.downX;
      const dy = e.clientY - s.downY;
      const dist = Math.sqrt(dx * dx + dy * dy);

      if (s.phase === "pending" && dist > MOVE_SLOP_PX) {
        if (portMoveRef.current) {
          s.phase = "port-move";
        } else if (wiringRef.current) {
          s.phase = "wiring";
        } else if (nodeDragRef.current) {
          s.phase = "dragging";
        } else if (s.emptyDown) {
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
            const w = node.data?.width ?? NODE_DIM_FALLBACK.width;
            const h = node.data?.height ?? NODE_DIM_FALLBACK.height;
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

      if (s.phase === "port-move" && portMoveRef.current) {
        // Slide the port along its node's ring: project the pointer ray onto the
        // z=0 plane through the node center, take the in-plane direction from center
        // to the hit, constrain to the ring (z=0). The port sphere AND the incident
        // edge follow ~1 frame behind via Go's re-emit (node-geometry + edge-geometry
        // streams) — same optimistic-follow path node-drag uses for the node body.
        const pm = portMoveRef.current;
        const rect = (e.currentTarget as HTMLDivElement).getBoundingClientRect();
        const planePoint = unprojectToPlane(e.clientX, e.clientY, rect);
        if (planePoint) {
          const anchor = pointerRingAnchor(pm.nodeCenter, planePoint);
          if (anchor) {
            schedulePortAnchor({
              nodeId: pm.nodeId,
              portName: pm.portName,
              isInput: pm.isInput,
              anchor,
            });
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
    [cameraRef, nodesRef, onMoveNode, scheduleNodeMove, schedulePortAnchor, unprojectToPlane],
  );

  const onPointerUp = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      const s = state.current;

      // Wiring completed: if dropped on a port, create an edge.
      // Edge creation runs for both "wiring" (full drag) and "pending" (short drag under MOVE_SLOP_PX).
      if (wiringRef.current !== null && (s.phase === "wiring" || s.phase === "pending")) {
        const src = wiringRef.current;
        const rect = (e.currentTarget as HTMLDivElement).getBoundingClientRect();
        const { ndcX, ndcY } = pixelToNDC(e.clientX, e.clientY, rect);
        const targetPortId = pickRequest.current?.(ndcX, ndcY, { portOnly: true }) ?? null;
        if (targetPortId !== null) {
          const tgt = parsePortId(targetPortId);
          if (tgt.nodeId !== src.nodeId) {
            if (!src.isInput && tgt.isInput) {
              storeCreateEdge(src.nodeId, src.portName, tgt.nodeId, tgt.portName);
            } else if (src.isInput && !tgt.isInput) {
              storeCreateEdge(tgt.nodeId, tgt.portName, src.nodeId, src.portName);
            }
            // else: both same direction → skip
          }
        }
        wiringRef.current = null;
        s.phase = "idle";
        (e.currentTarget as HTMLDivElement).releasePointerCapture(e.pointerId);
        return;
      }

      // Port-move completed: flush the final anchor (Go persists it; Phase 1 made
      // anchor round-trip through save). Reset throttle so the last frame isn't dropped.
      if (s.phase === "port-move" && portMoveRef.current) {
        const pm = portMoveRef.current;
        const rect = (e.currentTarget as HTMLDivElement).getBoundingClientRect();
        const planePoint = unprojectToPlane(e.clientX, e.clientY, rect);
        if (planePoint) {
          const anchor = pointerRingAnchor(pm.nodeCenter, planePoint);
          if (anchor) {
            pendingAnchor.current = null;
            anchorRafPending.current = false;
            flushPortAnchor({ nodeId: pm.nodeId, portName: pm.portName, isInput: pm.isInput, anchor });
          }
        }
        portMoveRef.current = null;
        s.phase = "idle";
        (e.currentTarget as HTMLDivElement).releasePointerCapture(e.pointerId);
        return;
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
    [flushNodeMove, flushPortAnchor, onMoveNode, onSelect, pickRequest, storeCreateEdge, unprojectToPlane],
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
