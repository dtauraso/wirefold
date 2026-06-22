// interaction-handlers.ts — pure ctx-threaded handler bodies for useInteractionControls.
// Each function takes a stable InteractionCtx (bundling every ref + stable callback the
// handler closes over) plus the event args, and reads/writes ctx.someRef.current exactly
// as the original inline hook code did. Behavior is identical: same reads, same writes,
// same order, same refs. The hook's useCallback wrappers stay thin and route through ctx.

import * as THREE from "three";
import type { RFNode, RFEdge, NodeData, EdgeData } from "../types";
import { nodeWorldPos, nodeRadius, pixelToNDC, pointerRingAnchor } from "./geometry-helpers";
import { patchViewerState } from "../state/viewer-state";
import { scheduleViewSave } from "../save";
import { useCursorStore } from "./cursor-store";
import { cameraFrame, screenToPolar, toWorld, arcAxisAngle, angleAboutAxis, rotateAboutAxis, deltaToPolar, planeSlide } from "./polar";
import { sendViewpointSet, sendViewpointOrbit, sendViewpointPan, worldDirToAngles } from "./viewpoint-bridge";
import type { ControlState, PickOptions } from "./interaction-controls";
import { computeContentSphere } from "./interaction-controls";

// ---------------------------------------------------------------------------
// Camera persistence helper
// ---------------------------------------------------------------------------

/** Write current camera position + quaternion to viewerState and schedule a save. */
export function commitCamera(cam: THREE.PerspectiveCamera) {
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
export function computeLargeSphereRadius(nodes: RFNode<NodeData>[]): number {
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
 * P7.5: Clamp the camera position so it stays on or inside the large
 * container sphere of radius R. If the camera is outside, project it back
 * to the surface (same direction, length = R). No-op when inside.
 */
export function constrainInsideLargeSphere(cam: THREE.PerspectiveCamera, R: number): void {
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

// ---------------------------------------------------------------------------
// InteractionCtx — stable bundle of refs + stable callbacks
// ---------------------------------------------------------------------------

export interface InteractionCtx {
  state: React.MutableRefObject<ControlState>;
  nodeDragRef: React.MutableRefObject<{
    nodeId: string;
    startCenter: THREE.Vector3;
    lastWorldTarget: THREE.Vector3 | null;
  } | null>;
  wiringRef: React.MutableRefObject<{ nodeId: string; portName: string; isInput: boolean } | null>;
  portMoveRef: React.MutableRefObject<{
    nodeId: string;
    portName: string;
    isInput: boolean;
    nodeCenter: THREE.Vector3;
  } | null>;
  pendingNodeMove: React.MutableRefObject<{ nodeId: string; x: number; y: number; z: number } | null>;
  rafPending: React.MutableRefObject<boolean>;
  pendingAnchor: React.MutableRefObject<{
    nodeId: string;
    portName: string;
    isInput: boolean;
    anchor: { x: number; y: number; z: number };
  } | null>;
  anchorRafPending: React.MutableRefObject<boolean>;
  cameraRef: React.MutableRefObject<THREE.PerspectiveCamera | null>;
  nodesRef: React.MutableRefObject<RFNode<NodeData>[]>;
  edgesRef: React.MutableRefObject<RFEdge<EdgeData>[]>;
  targetRef: React.MutableRefObject<THREE.Vector3>;
  pickRequest: React.MutableRefObject<((ndcX: number, ndcY: number, opts?: PickOptions) => string | null) | null>;
  onSelect: (id: string | null, ownSphere?: boolean) => void;
  storeCreateEdge: (sourceId: string, sourceHandle: string | null, targetId: string, targetHandle: string | null) => void;
  incidentEdgeIds: (nodeId: string, portName: string, isInput: boolean) => string[];
  scheduleNodeMove: (nodeId: string, x: number, y: number, z: number) => void;
  flushNodeMove: (nodeId: string, x: number, y: number, z: number) => void;
  schedulePortAnchor: (p: { nodeId: string; portName: string; isInput: boolean; anchor: { x: number; y: number; z: number } }) => void;
  flushPortAnchor: (p: { nodeId: string; portName: string; isInput: boolean; anchor: { x: number; y: number; z: number } }) => void;
  scheduleOrigin: (x: number, y: number, z: number) => void;
}

// ---------------------------------------------------------------------------
// ref-closing helpers (moved verbatim from the hook)
// ---------------------------------------------------------------------------

/** Parse a portId of the form `nodeId:in:portName` or `nodeId:out:portName`. */
export function parsePortId(pid: string): { nodeId: string; isInput: boolean; portName: string } {
  const i = pid.indexOf(":");
  const nodeId = pid.slice(0, i);
  const rest = pid.slice(i + 1);
  const j = rest.indexOf(":");
  const dir = rest.slice(0, j);
  const portName = rest.slice(j + 1);
  return { nodeId, isInput: dir === "in", portName };
}

/** Unproject a pointer position (client coords) to the z=0 world plane. */
export function unprojectToPlane(ctx: InteractionCtx, clientX: number, clientY: number, rect: DOMRect): THREE.Vector3 | null {
  const cam = ctx.cameraRef.current;
  if (!cam) return null;
  const { ndcX, ndcY } = pixelToNDC(clientX, clientY, rect);
  const raycaster = new THREE.Raycaster(); // polar-nav-ok: node-drag/port-move plane hit, not rotation
  raycaster.setFromCamera(new THREE.Vector2(ndcX, ndcY), cam);
  const plane = new THREE.Plane(new THREE.Vector3(0, 0, 1), 0);
  const target = new THREE.Vector3();
  const hit = raycaster.ray.intersectPlane(plane, target);
  return hit ? target : null;
}

/**
 * Bounding-box center of all node world positions. Bounded scene-focus point
 * used as a fallback anchor when the z=0 plane hit diverges (grazing view).
 * Returns (0,0,0) when there are no nodes.
 */
export function sceneCenter(ctx: InteractionCtx): THREE.Vector3 {
  const nodes = ctx.nodesRef.current;
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
}

/**
 * Ensure the persistent orbit/pan/dolly pivot (targetRef) is a finite world
 * point before a camera gesture. When uninitialized (NaN) or non-finite, seed
 * it by projecting sceneCenter() onto the camera's forward ray: target sits at
 * scene depth along the actual view direction. Fall back to sceneCenter(), then
 * the origin. Defined purely from (camera pose, scene) — no z=0 raycast.
 */
export function ensureTarget(ctx: InteractionCtx, cam: THREE.PerspectiveCamera): THREE.Vector3 {
  const t = ctx.targetRef.current;
  if (Number.isFinite(t.x) && Number.isFinite(t.y) && Number.isFinite(t.z)) return t;
  const TARGET_MIN = 10; // minimum forward depth so target never sits on the camera
  cam.updateMatrixWorld(true);
  const forward = new THREE.Vector3();
  cam.getWorldDirection(forward); // unit vector
  const center = sceneCenter(ctx);
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
}

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
export function regionFocus(ctx: InteractionCtx, cam: THREE.PerspectiveCamera): THREE.Vector3 {
  cam.updateMatrixWorld(true);
  const forward = new THREE.Vector3();
  cam.getWorldDirection(forward); // unit
  const nodes = ctx.nodesRef.current;
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
  if (zNear === Infinity || zFar === -Infinity) return ensureTarget(ctx, cam);
  const FOCUS_MIN = 10; // keep the pivot off the camera even if the slab is behind it
  const midDepth = Math.max((zNear + zFar) / 2, FOCUS_MIN);
  return cam.position.clone().add(forward.multiplyScalar(midDepth));
}

// ---------------------------------------------------------------------------
// pointer event handlers (bodies moved verbatim from the hook)
// ---------------------------------------------------------------------------

/**
 * Seed the content-sphere screen mapping used by BOTH rotation paths (empty-space
 * free-roll and handhold-constrained). Computes the world pivot (content-sphere center),
 * projects it to screen once for the pixel center, and the px-per-radian scale, then seeds
 * prevX/prevY. All sphere/angle math stays in polar.ts; this only reads camera state and
 * does the one allowed pivot projection. Mirrors the original inline empty-space block.
 */
function beginSphereRotation(
  ctx: InteractionCtx,
  s: ControlState,
  cam0: THREE.PerspectiveCamera,
  rect: DOMRect,
  clientX: number,
  clientY: number,
) {
  cam0.updateMatrixWorld(true);
  const cs = computeContentSphere(ctx.nodesRef.current);
  s.rotPivot = cs.center.clone();

  const pivotNdc = cs.center.clone().project(cam0); // polar-center-projection
  s.rotCx = ((pivotNdc.x + 1) / 2) * rect.width + rect.left;
  s.rotCy = ((-pivotNdc.y + 1) / 2) * rect.height + rect.top;

  const pivotDist = cam0.position.distanceTo(cs.center);
  const fovRad = (cam0.fov * Math.PI) / 180;
  const Rpx = (cs.radius / pivotDist) * (rect.height / 2) / Math.tan(fovRad / 2);
  s.rotPxPerRad = Math.max(Rpx * (2 / Math.PI), 1);

  s.prevX = clientX;
  s.prevY = clientY;
}

export function handlePointerDown(ctx: InteractionCtx, e: React.PointerEvent<HTMLDivElement>) {
  const s = ctx.state.current;
  s.downX = e.clientX;
  s.downY = e.clientY;
  s.downTime = Date.now();
  s.prevX = e.clientX;
  s.prevY = e.clientY;
  s.phase = "pending";
  s.emptyDown = false;
  s.handholdDown = false;
  s.rotAxis = null;

  // Clear previous drag/wiring state.
  ctx.nodeDragRef.current = null;
  ctx.wiringRef.current = null;
  ctx.portMoveRef.current = null;

  // Pick node under cursor.
  const rect = (e.currentTarget as HTMLDivElement).getBoundingClientRect();
  const { ndcX, ndcY } = pixelToNDC(e.clientX, e.clientY, rect);

  // Check for port hit. A CONNECTED port (has an incident edge) drags to MOVE the
  // port along its node's ring; an UNCONNECTED port drags port→port to WIRE a new
  // edge. Both resolve from a "pending" phase on first move past the slop.
  const portHit = ctx.pickRequest.current?.(ndcX, ndcY, { portOnly: true }) ?? null;
  if (portHit !== null) {
    const p = parsePortId(portHit);
    const connected = ctx.incidentEdgeIds(p.nodeId, p.portName, p.isInput).length > 0;
    if (connected) {
      const node = ctx.nodesRef.current.find((n) => n.id === p.nodeId);
      if (node) {
        ctx.portMoveRef.current = {
          nodeId: p.nodeId,
          portName: p.portName,
          isInput: p.isInput,
          nodeCenter: nodeWorldPos(node),
        };
      }
    } else {
      ctx.wiringRef.current = p;
    }
    s.phase = "pending";
    (e.currentTarget as HTMLDivElement).setPointerCapture(e.pointerId);
    return;
  }

  // Check for a handhold grab BEFORE the node pick: a handhold starts constrained
  // (locked-disk) rotation. Set up the same content-sphere mapping the empty-space
  // path uses; the first move locks the disk axis (see handlePointerMove).
  const handholdHit = ctx.pickRequest.current?.(ndcX, ndcY, { handholdOnly: true }) ?? null;
  if (handholdHit !== null) {
    const cam0 = ctx.cameraRef.current;
    if (cam0) {
      s.handholdDown = true;
      beginSphereRotation(ctx, s, cam0, rect, e.clientX, e.clientY);
    }
    s.phase = "pending";
    (e.currentTarget as HTMLDivElement).setPointerCapture(e.pointerId);
    return;
  }

  const hitId = ctx.pickRequest.current?.(ndcX, ndcY) ?? null;
  s.emptyDown = (hitId === null);

  if (hitId !== null) {
    // Node hit: record the drag origin. The node's start world center defines a
    // camera-facing drag plane; pointer moves unproject onto that plane to a WORLD
    // TARGET. Go projects that target onto the node's PARENT sphere and re-aims the
    // node's Dir in diameter steps (sphere-chain layout) — no lattice stepping here.
    const node = ctx.nodesRef.current.find((n) => n.id === hitId);
    const cam = ctx.cameraRef.current;
    if (node && cam) {
      ctx.nodeDragRef.current = {
        nodeId: hitId,
        startCenter: nodeWorldPos(node),
        lastWorldTarget: null,
      };
    }
  } else {
    // Empty-space pointer-down: compute the pivot and sphere screen mapping for
    // motion-driven rotation. All sphere/angle math goes through polar.ts; this
    // block only reads camera state and projects the pivot to screen once.
    const cam0 = ctx.cameraRef.current;
    if (cam0) {
      // Seed the content-sphere screen mapping (pivot, pixel center, px-per-radian,
      // prevX/prevY). Shared with the handhold path — see beginSphereRotation.
      const rect = (e.currentTarget as HTMLDivElement).getBoundingClientRect();
      beginSphereRotation(ctx, s, cam0, rect, e.clientX, e.clientY);
    }
  }

  (e.currentTarget as HTMLDivElement).setPointerCapture(e.pointerId);
}

export function handlePointerMove(ctx: InteractionCtx, e: React.PointerEvent<HTMLDivElement>) {
  const s = ctx.state.current;
  useCursorStore.getState().set(e.clientX, e.clientY, true); // feed the polar pan guide (hover too)
  if (s.phase === "idle") return;

  const dx = e.clientX - s.downX;
  const dy = e.clientY - s.downY;
  const dist = Math.sqrt(dx * dx + dy * dy);

  if (s.phase === "pending" && dist > MOVE_SLOP_PX) {
    if (ctx.portMoveRef.current) {
      s.phase = "port-move";
    } else if (ctx.wiringRef.current) {
      s.phase = "wiring";
    } else if (ctx.nodeDragRef.current) {
      s.phase = "dragging";
    } else if (s.handholdDown) {
      // Handhold grab: start constrained (locked-disk) rotation. Re-seed prevX/prevY
      // and clear any stale axis so the first move locks the disk from two fresh points.
      s.prevX = e.clientX;
      s.prevY = e.clientY;
      s.rotAxis = null;
      s.phase = "handhold-rotating";
    } else if (s.emptyDown) {
      // Empty-space drag: start motion-driven great-circle rotation.
      // prevX/prevY were seeded at pointer-down; re-seed here in case the pointer
      // moved during the slop window so the first frame arc is always tiny.
      s.prevX = e.clientX;
      s.prevY = e.clientY;
      s.phase = "rotating";
      // Seed Go with the current camera so its polar state matches whatever the camera
      // is now (covers prior local pan/zoom).
      const cam0 = ctx.cameraRef.current;
      if (cam0) {
        const pivot: [number, number, number] = [s.rotPivot.x, s.rotPivot.y, s.rotPivot.z];
        const r = cam0.position.distanceTo(s.rotPivot);
        const posDir = cam0.position.clone().sub(s.rotPivot).normalize();
        const upDir = cam0.up.clone().normalize();
        const pos = worldDirToAngles(posDir);
        const up = worldDirToAngles(upDir);
        sendViewpointSet(pivot, r, pos, up);
      }
    }
  }

  if (s.phase === "dragging" && ctx.nodeDragRef.current) {
    // Node drag (sphere-chain): unproject the pointer onto a CAMERA-FACING plane
    // through the node's start center, giving a free WORLD TARGET. Go projects it
    // onto the node's PARENT sphere and snaps the re-aimed Dir to the node's own
    // diameter steps — the quantization lives in Go now, not in a TS lattice. The
    // body repositions when Go re-emits node-geometry. No lattice / no zoom math.
    const nd = ctx.nodeDragRef.current;
    const cam = ctx.cameraRef.current;
    const rect = (e.currentTarget as HTMLDivElement).getBoundingClientRect();
    if (cam) {
      const { ndcX, ndcY } = pixelToNDC(e.clientX, e.clientY, rect);
      const raycaster = new THREE.Raycaster(); // polar-nav-ok: node-drag plane hit, not rotation
      raycaster.setFromCamera(new THREE.Vector2(ndcX, ndcY), cam);
      const normal = new THREE.Vector3();
      cam.getWorldDirection(normal); // plane faces the camera
      const plane = new THREE.Plane().setFromNormalAndCoplanarPoint(normal, nd.startCenter);
      const target = new THREE.Vector3();
      const hit = raycaster.ray.intersectPlane(plane, target);
      if (hit && Number.isFinite(target.x) && Number.isFinite(target.y) && Number.isFinite(target.z)) {
        nd.lastWorldTarget = target.clone();
        ctx.scheduleNodeMove(nd.nodeId, target.x, target.y, target.z);
      }
    }
    s.prevX = e.clientX;
    s.prevY = e.clientY;
  }

  if (s.phase === "port-move" && ctx.portMoveRef.current) {
    // Slide the port along its node's ring: project the pointer ray onto the
    // z=0 plane through the node center, take the in-plane direction from center
    // to the hit, constrain to the ring (z=0). The port sphere AND the incident
    // edge follow ~1 frame behind via Go's re-emit (node-geometry + edge-geometry
    // streams) — same optimistic-follow path node-drag uses for the node body.
    const pm = ctx.portMoveRef.current;
    const rect = (e.currentTarget as HTMLDivElement).getBoundingClientRect();
    const planePoint = unprojectToPlane(ctx, e.clientX, e.clientY, rect);
    if (planePoint) {
      const anchor = pointerRingAnchor(pm.nodeCenter, planePoint);
      if (anchor) {
        ctx.schedulePortAnchor({
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
    const cam = ctx.cameraRef.current;
    if (cam) {
      const C = s.rotPivot;
      const CF = cameraFrame(cam.quaternion, C, 1);
      const prev = screenToPolar(s.prevX - s.rotCx, s.prevY - s.rotCy, s.rotPxPerRad);
      const curr = screenToPolar(e.clientX - s.rotCx, e.clientY - s.rotCy, s.rotPxPerRad);
      const prevDir = toWorld(CF, prev).sub(C).normalize(); // unit world dir of prev cursor point
      const currDir = toWorld(CF, curr).sub(C).normalize(); // unit world dir of curr cursor point
      // Send orbit to Go: curr→prev matches today's arcAxisAngle(currDir, prevDir) convention.
      // Go owns the camera; CameraFromStore applies the result back to three.js.
      sendViewpointOrbit(worldDirToAngles(currDir), worldDirToAngles(prevDir));
      s.prevX = e.clientX;
      s.prevY = e.clientY;
    }
  }

  if (s.phase === "handhold-rotating") {
    // Constrained rotation: the rotation DISK is locked from the first two cursor
    // points and reused for the whole gesture. After locking, only the angle about
    // that fixed axis tracks the cursor — wherever it goes — so the turn is clean
    // (90° handholds give repeatable square turns). All axis/angle math is in polar.ts.
    const cam = ctx.cameraRef.current;
    if (cam) {
      const C = s.rotPivot;
      const CF = cameraFrame(cam.quaternion, C, 1);
      const prev = screenToPolar(s.prevX - s.rotCx, s.prevY - s.rotCy, s.rotPxPerRad);
      const curr = screenToPolar(e.clientX - s.rotCx, e.clientY - s.rotCy, s.rotPxPerRad);
      const prevDir = toWorld(CF, prev).sub(C);
      const currDir = toWorld(CF, curr).sub(C);
      // Lock the disk normal from the FIRST two points (the arc's axis); reuse it after.
      if (!s.rotAxis) {
        const { axis, angle } = arcAxisAngle(currDir, prevDir);
        if (angle > 1e-6 && Number.isFinite(axis.x)) s.rotAxis = axis.clone();
      }
      if (s.rotAxis) {
        // Rotation about the LOCKED axis carrying curr→prev (same follow convention as
        // the free path's arcAxisAngle(currDir, prevDir)).
        const angle = angleAboutAxis(currDir, prevDir, s.rotAxis);
        if (Math.abs(angle) > 1e-6) {
          const fwd = new THREE.Vector3();
          cam.getWorldDirection(fwd);
          const offset = cam.position.clone().sub(C);
          const newOffset = rotateAboutAxis(offset.clone().normalize(), s.rotAxis, angle).multiplyScalar(offset.length());
          const newFwd = rotateAboutAxis(fwd, s.rotAxis, angle);
          const newUp = rotateAboutAxis(cam.up.clone().normalize(), s.rotAxis, angle);
          cam.position.copy(C).add(newOffset);
          cam.up.copy(newUp);
          cam.lookAt(cam.position.clone().add(newFwd));
          cam.updateMatrixWorld(true);
        }
      }
      s.prevX = e.clientX;
      s.prevY = e.clientY;
      commitCamera(cam);
    }
  }
}

export function handlePointerUp(ctx: InteractionCtx, e: React.PointerEvent<HTMLDivElement>) {
  const s = ctx.state.current;

  // Wiring completed: if dropped on a port, create an edge.
  // Edge creation runs for both "wiring" (full drag) and "pending" (short drag under MOVE_SLOP_PX).
  if (ctx.wiringRef.current !== null && (s.phase === "wiring" || s.phase === "pending")) {
    const src = ctx.wiringRef.current;
    const rect = (e.currentTarget as HTMLDivElement).getBoundingClientRect();
    const { ndcX, ndcY } = pixelToNDC(e.clientX, e.clientY, rect);
    const targetPortId = ctx.pickRequest.current?.(ndcX, ndcY, { portOnly: true }) ?? null;
    if (targetPortId !== null) {
      const tgt = parsePortId(targetPortId);
      if (tgt.nodeId !== src.nodeId) {
        if (!src.isInput && tgt.isInput) {
          ctx.storeCreateEdge(src.nodeId, src.portName, tgt.nodeId, tgt.portName);
        } else if (src.isInput && !tgt.isInput) {
          ctx.storeCreateEdge(tgt.nodeId, tgt.portName, src.nodeId, src.portName);
        }
        // else: both same direction → skip
      }
    }
    ctx.wiringRef.current = null;
    s.phase = "idle";
    (e.currentTarget as HTMLDivElement).releasePointerCapture(e.pointerId);
    return;
  }

  // Port-move completed: flush the final anchor (Go persists it; Phase 1 made
  // anchor round-trip through save). Reset throttle so the last frame isn't dropped.
  if (s.phase === "port-move" && ctx.portMoveRef.current) {
    const pm = ctx.portMoveRef.current;
    const rect = (e.currentTarget as HTMLDivElement).getBoundingClientRect();
    const planePoint = unprojectToPlane(ctx, e.clientX, e.clientY, rect);
    if (planePoint) {
      const anchor = pointerRingAnchor(pm.nodeCenter, planePoint);
      if (anchor) {
        ctx.pendingAnchor.current = null;
        ctx.anchorRafPending.current = false;
        ctx.flushPortAnchor({ nodeId: pm.nodeId, portName: pm.portName, isInput: pm.isInput, anchor });
      }
    }
    ctx.portMoveRef.current = null;
    s.phase = "idle";
    (e.currentTarget as HTMLDivElement).releasePointerCapture(e.pointerId);
    return;
  }

  // Node drag completed: flush the final WORLD target. Go projects it onto the
  // node's parent sphere, re-aims the node's Dir in diameter steps, persists Dir
  // (meta.json), re-propagates, and re-streams the centers — Go owns the snapped
  // position, so TS does not write view-node x/y here. Reset throttle so the last
  // frame isn't dropped.
  if (s.phase === "dragging" && ctx.nodeDragRef.current) {
    const nd = ctx.nodeDragRef.current;
    const t = nd.lastWorldTarget;
    if (t && Number.isFinite(t.x) && Number.isFinite(t.y) && Number.isFinite(t.z)) {
      ctx.pendingNodeMove.current = null;
      ctx.rafPending.current = false;
      ctx.flushNodeMove(nd.nodeId, t.x, t.y, t.z);
    }
    ctx.nodeDragRef.current = null;
    s.phase = "idle";
    return;
  }

  // Rotation completed (free OR handhold-constrained): reset without triggering select.
  if (s.phase === "rotating" || s.phase === "handhold-rotating") {
    ctx.nodeDragRef.current = null;
    s.handholdDown = false;
    s.rotAxis = null;
    s.phase = "idle";
    return;
  }

  ctx.nodeDragRef.current = null;

  if (s.phase === "pending") {
    const elapsed = Date.now() - s.downTime;
    const ddx = e.clientX - s.downX;
    const ddy = e.clientY - s.downY;
    const clickDist = Math.sqrt(ddx * ddx + ddy * ddy);
    if (elapsed < CLICK_MAX_MS && clickDist < MOVE_SLOP_PX) {
      // CLICK → pick (selects node or deselects on empty space)
      const rect = (e.currentTarget as HTMLDivElement).getBoundingClientRect();
      const { ndcX, ndcY } = pixelToNDC(e.clientX, e.clientY, rect);
      const hitId = ctx.pickRequest.current?.(ndcX, ndcY) ?? null;
      // Two-finger tap on a trackpad arrives as a secondary click (button 2) ->
      // select with the node's OWN sphere. A single (primary) click selects with
      // the spheres the node sits on the surface of.
      ctx.onSelect(hitId, e.button === 2);
    }
  }

  s.phase = "idle";
}

export function handleWheelNative(ctx: InteractionCtx, e: WheelEvent) {
  const cam = ctx.cameraRef.current;
  if (!cam) return;
  // Prevent browser scroll / back-nav gestures (requires non-passive listener).
  e.preventDefault();

  const up = worldDirToAngles(cam.up.clone().normalize());

  if (e.ctrlKey) {
    // ZOOM TO CURSOR is a DOLLY, which in the polar model is a PAN (a pivot translation),
    // not a radius change: translating the eye and pivot by the same world vector keeps r
    // and pos fixed, so the view direction never changes (no re-aim/flip) and the node
    // under the cursor stays put. delta = (1-factor)(P - eye) steps the eye a `factor`
    // amount along eye→P; floored so a zoom-in never reaches P. We seed Go with the
    // current camera (covers any local drift), then send the pan — Go owns the dolly.
    const ZOOM_BASE = 1.01;
    const MIN_DIST = 5;
    const wrect = (e.currentTarget as HTMLElement).getBoundingClientRect();
    const mouseNdcX = ((e.clientX - wrect.left) / wrect.width) * 2 - 1;
    const mouseNdcY = -(((e.clientY - wrect.top) / wrect.height) * 2 - 1);
    let cursorNdcDist = Infinity;
    let target: THREE.Vector3 | null = null;
    for (const n of ctx.nodesRef.current) {
      const c = nodeWorldPos(n);
      if (!Number.isFinite(c.x) || !Number.isFinite(c.y) || !Number.isFinite(c.z)) continue;
      const ndc = c.clone().project(cam);
      if (ndc.z > 1) continue; // behind the camera / beyond far plane
      const d = Math.hypot(ndc.x - mouseNdcX, ndc.y - mouseNdcY);
      if (d < cursorNdcDist) { cursorNdcDist = d; target = c; }
    }
    const P = target ?? regionFocus(ctx, cam); // node to dolly toward
    const toP = P.clone().sub(cam.position);
    const distP = toP.length();
    let factor = Math.pow(ZOOM_BASE, e.deltaY);
    if (distP * factor < MIN_DIST) factor = MIN_DIST / distP; // never reach P
    const delta = toP.multiplyScalar(1 - factor); // eye step along eye→P
    const pivot = regionFocus(ctx, cam);
    const r = cam.position.distanceTo(pivot);
    const pos = worldDirToAngles(cam.position.clone().sub(pivot).normalize());
    sendViewpointSet([pivot.x, pivot.y, pivot.z], r, pos, up);
    sendViewpointPan(delta.x, delta.y, delta.z);
  } else {
    const pivot = regionFocus(ctx, cam);
    const r = cam.position.distanceTo(pivot);
    const pos = worldDirToAngles(cam.position.clone().sub(pivot).normalize());
    // PAN = 2D screen-space slide along the camera's right/up basis. The world delta is
    // computed locally (camera pose + view depth) and handed to Go; Go moves the pivot
    // and returns the new camera state.
    const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
    const fovRad = (cam.fov * Math.PI) / 180;
    const worldPerPixel = (2 * r * Math.tan(fovRad / 2)) / rect.height;
    const { r: pr, angle } = deltaToPolar(e.deltaX, -e.deltaY);
    const delta = planeSlide(cam.quaternion, pr, angle, worldPerPixel);

    sendViewpointSet([pivot.x, pivot.y, pivot.z], r, pos, up);
    sendViewpointPan(delta.x, delta.y, delta.z);

    // Re-base the polar layout origin at the new screen-center (throttled per frame).
    if (Number.isFinite(pivot.x) && Number.isFinite(pivot.y) && Number.isFinite(pivot.z)) {
      ctx.scheduleOrigin(pivot.x, pivot.y, pivot.z);
    }
  }
}
