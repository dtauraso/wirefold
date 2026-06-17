// interaction-controls.ts — useInteractionControls hook and related types.
// Handles CLICK, NODE-DRAG, and SCROLL (dolly/pan) detection.
// Single-pointer empty-space drag (arcball rotation) and dwell→PanPad are removed.

import { useRef, useCallback } from "react";
import * as THREE from "three";
import type { RFNode, RFEdge, NodeData, EdgeData } from "../types";
import type { MoveEntry } from "../../messages";
import { nodeWorldPos, nodeRadius, pixelToNDC, pointerRingAnchor } from "./geometry-helpers";
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
// P7.5 — Large sphere constraint helpers
// ---------------------------------------------------------------------------

/**
 * Compute the radius of the large container sphere from the node set.
 * Mirrors the Go formula: max distance from origin to any node center + that
 * node's own radius, plus a 20% padding. Falls back to a minimum of 500 so
 * the camera is never clamped out of a scene with no nodes.
 */
function computeLargeSphereRadius(nodes: RFNode<NodeData>[]): number {
  const MIN_R = 500;
  if (!nodes || nodes.length === 0) return MIN_R;
  let maxR = 0;
  for (const n of nodes) {
    const p = nodeWorldPos(n);
    if (!Number.isFinite(p.x) || !Number.isFinite(p.y) || !Number.isFinite(p.z)) continue;
    const dist = p.length() + nodeRadius(n);
    if (dist > maxR) maxR = dist;
  }
  return Math.max(maxR * 1.2, MIN_R);
}

/**
 * The diagram's WORLD-FIXED content sphere: center = bounding-box center of the node
 * world positions, radius = farthest node from that center (+10% margin). This is the
 * arcball — fixed in world space, so it zooms WITH the diagram (both grow as you dolly
 * in) instead of staying screen-size. Exported shape used by the visible sphere too.
 */
export function computeContentSphere(nodes: RFNode<NodeData>[]): { center: THREE.Vector3; radius: number } {
  const center = new THREE.Vector3();
  if (!nodes || nodes.length === 0) return { center, radius: 100 };
  const min = new THREE.Vector3(Infinity, Infinity, Infinity);
  const max = new THREE.Vector3(-Infinity, -Infinity, -Infinity);
  for (const n of nodes) {
    const p = nodeWorldPos(n);
    if (!Number.isFinite(p.x) || !Number.isFinite(p.y) || !Number.isFinite(p.z)) continue;
    min.min(p); max.max(p);
  }
  center.addVectors(min, max).multiplyScalar(0.5);
  let r = 0;
  for (const n of nodes) {
    const p = nodeWorldPos(n);
    if (!Number.isFinite(p.x) || !Number.isFinite(p.y) || !Number.isFinite(p.z)) continue;
    r = Math.max(r, p.distanceTo(center));
  }
  return { center, radius: Math.max(r * 1.1, 1) };
}

/**
 * P7.5: Clamp the camera position so it stays on or inside the large
 * container sphere of radius R. If the camera is outside, project it back
 * to the surface (same direction, length = R). No-op when inside.
 */
function constrainInsideLargeSphere(cam: THREE.PerspectiveCamera, R: number): void {
  const len = cam.position.length();
  if (Number.isFinite(len) && len > R) {
    cam.position.multiplyScalar(R / len);
    cam.updateMatrixWorld(true);
  }
}

// ---------------------------------------------------------------------------
// Gesture discrimination constants
// ---------------------------------------------------------------------------

/** Max pointer-down → up duration (ms) to count as a CLICK. */
const CLICK_MAX_MS = 150;
/** Pixel movement threshold between CLICK and DRAG. */
const MOVE_SLOP_PX = 6;

/** Arcball sphere radius as a fraction of camera→pivot distance (<1 ⇒ camera outside
 *  the ball; large enough that the ball fills the view so there is no on-screen dead
 *  zone). Exported so the visible arcball sphere matches the grab sphere. */
export const ARCBALL_FILL = 0.4;

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
  // Arcball gesture-start snapshot
  arcPivot: THREE.Vector3;        // orbit pivot (screen-center scene point), fixed for the gesture
  arcP0: THREE.Vector3;           // unit sphere point at gesture start
  arcStartOffset: THREE.Vector3; // cam.position - C at gesture start
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
  onSelect: (id: string | null, ownSphere?: boolean) => void,
  nodesRef: React.MutableRefObject<RFNode<NodeData>[]>,
  storeCreateEdge: (sourceId: string, sourceHandle: string | null, targetId: string, targetHandle: string | null) => void,
  selectedIdRef: React.MutableRefObject<string | null>,
  edgesRef: React.MutableRefObject<RFEdge<EdgeData>[]>,
  targetRef: React.MutableRefObject<THREE.Vector3>,
) {
  const state = useRef<ControlState>({
    phase: "idle",
    downX: 0, downY: 0, downTime: 0,
    prevX: 0, prevY: 0,
    emptyDown: false,
    arcPivot: new THREE.Vector3(),
    arcP0: new THREE.Vector3(),
    arcStartOffset: new THREE.Vector3(),
    arcStartUp: new THREE.Vector3(0, 1, 0),
    arcStartQuat: new THREE.Quaternion(),
  });

  // Node-drag state: set when pointer-down lands on a node.
  const nodeDragRef = useRef<{
    nodeId: string;
    startCenter: THREE.Vector3; // node world center at drag start (defines the drag plane)
    lastWorldTarget: THREE.Vector3 | null; // most recent world target sent during the drag
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
  const pendingNodeMove = useRef<{ nodeId: string; x: number; y: number; z: number } | null>(null);
  const rafPending = useRef(false);

  const flushNodeMove = useCallback((nodeId: string, x: number, y: number, z: number) => {
    // Decentralized node-move: mail-sort the move to the moved node + every incident
    // edge (source===moved || target===moved). TS owns the graph and computes the
    // incident edges; Go's per-node/per-edge goroutines own the recompute. Every
    // entry carries the same moved node id + WORLD-SPACE target (x,y,z) — Go snaps it
    // to the nearest lattice cell (TS computes no cell). Keys are node id + edge ids.
    const entry: MoveEntry = { nodeId, x, y, z };
    const entries: Record<string, MoveEntry> = { [nodeId]: entry };
    for (const e of edgesRef.current) {
      if (e.source === nodeId || e.target === nodeId) {
        entries[e.id] = entry;
      }
    }
    vscode.postMessage({ type: "edit", op: "update", entries });
  }, [edgesRef]);

  const scheduleNodeMove = useCallback((nodeId: string, x: number, y: number, z: number) => {
    pendingNodeMove.current = { nodeId, x, y, z };
    if (!rafPending.current) {
      rafPending.current = true;
      requestAnimationFrame(() => {
        rafPending.current = false;
        const p = pendingNodeMove.current;
        if (p) {
          flushNodeMove(p.nodeId, p.x, p.y, p.z);
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

  // Throttle set-origin IPC: one message per animation frame during pan.
  const pendingOrigin = useRef<{ x: number; y: number; z: number } | null>(null);
  const originRafPending = useRef(false);

  const flushOrigin = useCallback((x: number, y: number, z: number) => {
    vscode.postMessage({ type: "edit", op: "set-origin", x, y, z });
  }, []);

  const scheduleOrigin = useCallback((x: number, y: number, z: number) => {
    pendingOrigin.current = { x, y, z };
    if (!originRafPending.current) {
      originRafPending.current = true;
      requestAnimationFrame(() => {
        originRafPending.current = false;
        const p = pendingOrigin.current;
        if (p) {
          flushOrigin(p.x, p.y, p.z);
          pendingOrigin.current = null;
        }
      });
    }
  }, [flushOrigin]);

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

  /**
   * Bounding-box center of all node world positions. Bounded scene-focus point
   * used as a fallback anchor when the z=0 plane hit diverges (grazing view).
   * Returns (0,0,0) when there are no nodes.
   */
  const sceneCenter = useCallback((): THREE.Vector3 => {
    const nodes = nodesRef.current;
    if (!nodes || nodes.length === 0) return new THREE.Vector3(0, 0, 0);
    const min = new THREE.Vector3(Infinity, Infinity, Infinity);
    const max = new THREE.Vector3(-Infinity, -Infinity, -Infinity);
    let any = false;
    for (const n of nodes) {
      const p = nodeWorldPos(n);
      if (!Number.isFinite(p.x) || !Number.isFinite(p.y) || !Number.isFinite(p.z)) continue;
      min.min(p);
      max.max(p);
      any = true;
    }
    if (!any) return new THREE.Vector3(0, 0, 0);
    return min.add(max).multiplyScalar(0.5);
  }, [nodesRef]);

  /**
   * Ensure the persistent orbit/pan/dolly pivot (targetRef) is a finite world
   * point before a camera gesture. When uninitialized (NaN) or non-finite, seed
   * it by projecting sceneCenter() onto the camera's forward ray: target sits at
   * scene depth along the actual view direction. Fall back to sceneCenter(), then
   * the origin. Defined purely from (camera pose, scene) — no z=0 raycast.
   */
  const ensureTarget = useCallback(
    (cam: THREE.PerspectiveCamera): THREE.Vector3 => {
      const t = targetRef.current;
      if (Number.isFinite(t.x) && Number.isFinite(t.y) && Number.isFinite(t.z)) return t;
      const TARGET_MIN = 10; // minimum forward depth so target never sits on the camera
      cam.updateMatrixWorld(true);
      const forward = new THREE.Vector3();
      cam.getWorldDirection(forward); // unit vector
      const center = sceneCenter();
      const proj = forward.dot(center.clone().sub(cam.position));
      const depth = Number.isFinite(proj) ? Math.max(proj, TARGET_MIN) : TARGET_MIN;
      const seed = cam.position.clone().add(forward.multiplyScalar(depth));
      if (Number.isFinite(seed.x) && Number.isFinite(seed.y) && Number.isFinite(seed.z)) {
        t.copy(seed);
      } else if (Number.isFinite(center.x) && Number.isFinite(center.y) && Number.isFinite(center.z)) {
        t.copy(center);
      } else {
        t.set(0, 0, 0);
      }
      return t;
    },
    [targetRef, sceneCenter],
  );

  /**
   * Region-center rotation focus (model B). The diagram occupies a depth SLAB in
   * front of the camera: zNear = the nearest node's depth along the camera's forward
   * axis, zFar = the farthest node's. The rotation pivot is the CENTER of that slab —
   * a point straight ahead of the camera (on the forward ray, i.e. screen center) at
   * depth (zNear+zFar)/2. It is camera-relative: it does NOT track the diagram
   * sideways. You pan the diagram to bring whatever part you want into the region,
   * then orbit around it. Recomputed at each rotation start so the depth range is
   * current. Falls back to ensureTarget() when there are no finite node depths.
   */
  const regionFocus = useCallback(
    (cam: THREE.PerspectiveCamera): THREE.Vector3 => {
      cam.updateMatrixWorld(true);
      const forward = new THREE.Vector3();
      cam.getWorldDirection(forward); // unit
      const nodes = nodesRef.current;
      let zNear = Infinity;
      let zFar = -Infinity;
      if (nodes) {
        for (const n of nodes) {
          const p = nodeWorldPos(n);
          if (!Number.isFinite(p.x) || !Number.isFinite(p.y) || !Number.isFinite(p.z)) continue;
          const depth = forward.dot(p.clone().sub(cam.position));
          if (!Number.isFinite(depth)) continue;
          if (depth < zNear) zNear = depth;
          if (depth > zFar) zFar = depth;
        }
      }
      if (zNear === Infinity || zFar === -Infinity) return ensureTarget(cam);
      const FOCUS_MIN = 10; // keep the pivot off the camera even if the slab is behind it
      const midDepth = Math.max((zNear + zFar) / 2, FOCUS_MIN);
      return cam.position.clone().add(forward.multiplyScalar(midDepth));
    },
    [nodesRef, ensureTarget],
  );

  // TRUE 3-D arcball: a real sphere at the pivot C, sized so the camera is OUTSIDE it
  // and it fills the view (R = ARCBALL_FILL·dist, < dist). Raycast the cursor onto it;
  // the hit is the grabbed world-space unit direction from C. Off the silhouette, use
  // the limb (nearest sphere point to the ray) → roll. No 2-D screen projection, no
  // hemisphere/dome, no z=0 rim — you grab the actual sphere, so there is no dead zone
  // anywhere the sphere covers the view.
  const arcballPoint = useCallback(
    (clientX: number, clientY: number, rect: DOMRect, cam: THREE.PerspectiveCamera): THREE.Vector3 => {
      const { center: C, radius: R } = computeContentSphere(nodesRef.current);
      const { ndcX, ndcY } = pixelToNDC(clientX, clientY, rect);
      const ray = new THREE.Raycaster();
      ray.setFromCamera(new THREE.Vector2(ndcX, ndcY), cam);
      const hit = new THREE.Vector3();
      if (ray.ray.intersectSphere(new THREE.Sphere(C, R), hit)) {
        return hit.sub(C).normalize();
      }
      const foot = new THREE.Vector3();
      ray.ray.closestPointToPoint(C, foot);
      return foot.sub(C).normalize();
    },
    [nodesRef],
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
        // Node hit: record the drag origin. The node's start world center defines a
        // camera-facing drag plane; pointer moves unproject onto that plane to a WORLD
        // TARGET. Go projects that target onto the node's PARENT sphere and re-aims the
        // node's Dir in diameter steps (sphere-chain layout) — no lattice stepping here.
        const node = nodesRef.current.find((n) => n.id === hitId);
        const cam = cameraRef.current;
        if (node && cam) {
          nodeDragRef.current = {
            nodeId: hitId,
            startCenter: nodeWorldPos(node),
            lastWorldTarget: null,
          };
        }
      } else {
        // Empty-space pointerdown: snapshot camera state for true arcball rotation.
        const cam0 = cameraRef.current;
        if (cam0) {
          cam0.updateMatrixWorld(true);
          // Pivot = the scene point straight ahead (screen center), snapshotted now and
          // held for the whole gesture. It shares the arcball's viewport center, so
          // rotation orbits what you're looking at instead of a point that drifts to the
          // side as you pan. Panning changes what's at center → next gesture pivots there.
          const C = computeContentSphere(nodesRef.current).center;
          s.arcPivot = C.clone();
          s.arcStartOffset = cam0.position.clone().sub(C);
          s.arcStartUp = cam0.up.clone();
          s.arcStartQuat = cam0.quaternion.clone();
          const rect = (e.currentTarget as HTMLDivElement).getBoundingClientRect();
          s.arcP0 = arcballPoint(e.clientX, e.clientY, rect, cam0);
        }
      }

      (e.currentTarget as HTMLDivElement).setPointerCapture(e.pointerId);
    },
    [cameraRef, nodesRef, pickRequest, incidentEdgeIds, arcballPoint],
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
        // Node drag (sphere-chain): unproject the pointer onto a CAMERA-FACING plane
        // through the node's start center, giving a free WORLD TARGET. Go projects it
        // onto the node's PARENT sphere and snaps the re-aimed Dir to the node's own
        // diameter steps — the quantization lives in Go now, not in a TS lattice. The
        // body repositions when Go re-emits node-geometry. No lattice / no zoom math.
        const nd = nodeDragRef.current;
        const cam = cameraRef.current;
        const rect = (e.currentTarget as HTMLDivElement).getBoundingClientRect();
        if (cam) {
          const { ndcX, ndcY } = pixelToNDC(e.clientX, e.clientY, rect);
          const raycaster = new THREE.Raycaster();
          raycaster.setFromCamera(new THREE.Vector2(ndcX, ndcY), cam);
          const normal = new THREE.Vector3();
          cam.getWorldDirection(normal); // plane faces the camera
          const plane = new THREE.Plane().setFromNormalAndCoplanarPoint(normal, nd.startCenter);
          const target = new THREE.Vector3();
          const hit = raycaster.ray.intersectPlane(plane, target);
          if (hit && Number.isFinite(target.x) && Number.isFinite(target.y) && Number.isFinite(target.z)) {
            nd.lastWorldTarget = target.clone();
            scheduleNodeMove(nd.nodeId, target.x, target.y, target.z);
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
          const C = s.arcPivot; // fixed pivot (screen-center scene point) for the gesture
          const rect = (e.currentTarget as HTMLDivElement).getBoundingClientRect();
          const p1 = arcballPoint(e.clientX, e.clientY, rect, cam);
          // arcP0/p1 are WORLD-space unit directions on the real sphere about C. The
          // camera orbits by the rotation that makes the grabbed point follow the
          // cursor: rotate the camera by setFromUnitVectors(p1, p0) around C — the whole
          // rigid scene turns as one, roll included at the silhouette. Swap (p1,p0)↔(p0,p1)
          // to flip rotation direction if needed.
          const rotInv = new THREE.Quaternion().setFromUnitVectors(p1, s.arcP0);
          cam.position.copy(C).add(s.arcStartOffset.clone().applyQuaternion(rotInv));
          cam.quaternion.copy(rotInv).multiply(s.arcStartQuat);
          cam.up.copy(s.arcStartUp.clone().applyQuaternion(rotInv));
          cam.updateMatrixWorld(true);
          // P7.5: keep camera inside the large sphere after orbit.
          constrainInsideLargeSphere(cam, computeLargeSphereRadius(nodesRef.current));
          commitCamera(cam);
        }
      }
    },
    [cameraRef, nodesRef, scheduleNodeMove, schedulePortAnchor, unprojectToPlane, arcballPoint],
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

      // Node drag completed: flush the final WORLD target. Go projects it onto the
      // node's parent sphere, re-aims the node's Dir in diameter steps, persists Dir
      // (meta.json), re-propagates, and re-streams the centers — Go owns the snapped
      // position, so TS does not write view-node x/y here. Reset throttle so the last
      // frame isn't dropped.
      if (s.phase === "dragging" && nodeDragRef.current) {
        const nd = nodeDragRef.current;
        const t = nd.lastWorldTarget;
        if (t && Number.isFinite(t.x) && Number.isFinite(t.y) && Number.isFinite(t.z)) {
          pendingNodeMove.current = null;
          rafPending.current = false;
          flushNodeMove(nd.nodeId, t.x, t.y, t.z);
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
          // Two-finger tap on a trackpad arrives as a secondary click (button 2) ->
          // select with the node's OWN sphere. A single (primary) click selects with
          // the spheres the node sits on the surface of.
          onSelect(hitId, e.button === 2);
        }
      }

      s.phase = "idle";
    },
    [flushNodeMove, flushPortAnchor, onSelect, pickRequest, storeCreateEdge, unprojectToPlane],
  );

  // Exposed so ThreeView can attach a non-passive native listener.
  const onWheelNative = useCallback(
    (e: WheelEvent) => {
      const cam = cameraRef.current;
      if (!cam) return;
      // Prevent browser scroll / back-nav gestures (requires non-passive listener).
      e.preventDefault();

      if (e.ctrlKey) {
        // DOLLY toward the persistent target: scale the camera's offset from the
        // target multiplicatively. Defined purely from (camera pose, target) — both
        // finite — so it's a total operation with no z=0 raycast. >1 deltaY zooms
        // out. MIN_DIST floors |offset| so the camera never reaches/crosses target.
        //
        // RE-AIM: before zooming, snap the persistent target onto the NODE NEAREST
        // THE CURSOR — zoom in toward the node the mouse is on. Convert the pointer to
        // NDC, project each node's world center to NDC, and pick the one with the
        // smallest distance from the cursor's NDC. This always selects a real node
        // (never an edge, never null, never an occluding neighbor), and zooming toward
        // it keeps that node under the cursor. If no node is finite/in-front, we leave
        // targetRef as-is and zoom toward the existing persistent focus — no z=0 plane
        // dependency either way.
        const wrect = (e.currentTarget as HTMLElement).getBoundingClientRect();
        const mouseNdcX = ((e.clientX - wrect.left) / wrect.width) * 2 - 1;
        const mouseNdcY = -(((e.clientY - wrect.top) / wrect.height) * 2 - 1);
        let cursorNode: string | null = null;
        let cursorNdcDist = Infinity;
        let cursorPos: THREE.Vector3 | null = null;
        for (const n of nodesRef.current) {
          const c = nodeWorldPos(n);
          if (!Number.isFinite(c.x) || !Number.isFinite(c.y) || !Number.isFinite(c.z)) continue;
          const ndc = c.clone().project(cam);
          if (ndc.z > 1) continue; // behind the camera / beyond far plane
          const dx = ndc.x - mouseNdcX;
          const dy = ndc.y - mouseNdcY;
          const d = Math.sqrt(dx * dx + dy * dy);
          if (d < cursorNdcDist) {
            cursorNdcDist = d;
            cursorNode = n.id;
            cursorPos = c;
          }
        }
        if (cursorPos) {
          targetRef.current.copy(cursorPos);
        }
        const target = ensureTarget(cam);
        const ZOOM_BASE = 1.01;
        const MIN_DIST = 5;
        const factor = Math.pow(ZOOM_BASE, e.deltaY);
        const offset = cam.position.clone().sub(target);
        const len = offset.length();
        if (Number.isFinite(len) && len > 1e-9) {
          let f = factor;
          if (len * f < MIN_DIST) f = MIN_DIST / len;
          if (Number.isFinite(f)) {
            cam.position.copy(target).add(offset.multiplyScalar(f));
          }
        }
        // P7.5: keep camera inside the large sphere after dolly.
        constrainInsideLargeSphere(cam, computeLargeSphereRadius(nodesRef.current));
      } else {
        // PAN = pure 2D screen-space translation along the camera's own right/up
        // basis, translating BOTH the camera AND the persistent target by the same
        // vector (target rides along, so the look-direction is preserved). The only
        // depth is `dist` = |camPos - target| — finite, stable for the whole gesture,
        // no z=0 raycast. Defined purely from (camera pose, target).
        const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
        const target = ensureTarget(cam);

        // Camera basis in world space (orientation unchanged by pan).
        const right = new THREE.Vector3().setFromMatrixColumn(cam.matrixWorld, 0);
        const up = new THREE.Vector3().setFromMatrixColumn(cam.matrixWorld, 1);

        const dist = cam.position.distanceTo(target);

        // Perspective world units per screen pixel at `dist` (vertical FOV).
        const fovRad = (cam.fov * Math.PI) / 180;
        const worldPerPixel = (2 * dist * Math.tan(fovRad / 2)) / rect.height;

        // deltaY positive = scroll down. Slide camera + target along the view plane.
        // These two signs are the per-axis flip point if a direction still feels wrong.
        const delta = right
          .multiplyScalar(e.deltaX * worldPerPixel)
          .add(up.multiplyScalar(-e.deltaY * worldPerPixel));
        cam.position.add(delta);
        target.add(delta);

        // Square-on check: cam looks down -z, so right=(1,0,0), up=(0,1,0). Then this
        // is a world-XY translation scaled by worldPerPixel at the look-distance —
        // exactly the prior square-on behavior (a flat 2D slide in the z=0 plane).
        // P7.5: keep camera inside the large sphere after pan.
        constrainInsideLargeSphere(cam, computeLargeSphereRadius(nodesRef.current));
        // Re-form the polar origin at the new screen-center: compute the world point
        // straight ahead of the camera (regionFocus) and send set-origin to Go.
        // Throttled to one per animation frame so a scroll burst sends at most one.
        const newFocus = regionFocus(cam);
        if (Number.isFinite(newFocus.x) && Number.isFinite(newFocus.y) && Number.isFinite(newFocus.z)) {
          scheduleOrigin(newFocus.x, newFocus.y, newFocus.z);
        }
      }
      // Commit camera position after each wheel step (scheduleViewSave debounces).
      commitCamera(cam);
    },
    [cameraRef, ensureTarget, pickRequest, nodesRef, targetRef, regionFocus, scheduleOrigin],
  );

  return { onPointerDown, onPointerMove, onPointerUp, onWheelNative };
}
