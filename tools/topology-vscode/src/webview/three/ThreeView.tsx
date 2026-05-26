// ThreeView — 3D replica of the RF graph with interaction grammar:
//   - PerspectiveCamera (parallax + depth cues)
//   - Click → raycast pick/select
//   - Drag → arcball rotation (pivot at picked 3D point or scene center)
//   - Scroll → dolly along view axis
//   - Roll slider widget (screen-plane camera roll)
//   - Dolly buttons (hold-to-dolly)
//   - Pan pad (200ms dwell over empty canvas → draggable pad)

import { useEffect, useRef, useState, useCallback, useMemo } from "react";
import { Canvas, useThree, useFrame } from "@react-three/fiber";
import * as THREE from "three";
import type { RFNode, RFEdge, NodeData, EdgeData } from "../types";
import { useThreeStore } from "./store";
import { getPulseMap, claimDelivered } from "./pulse-state";
import { vscode } from "../vscode-api";
import { getPauseAdjustedNow } from "../state/run-status";
import { pushSnapshot, undo, redo } from "../state/history";
import { patchViewerState } from "../state/viewer-state";
import { scheduleSave, scheduleViewSave } from "../save";

// ---------------------------------------------------------------------------
// Label LOD constants
// ---------------------------------------------------------------------------

/** Show label for the N nodes nearest to the camera, in addition to hovered/selected. */
const NEAREST_N = 8;

/** Validation flag colors. */
const FLAG_FILL = "#c62828";
const FLAG_RING = "#ff5252";
const FLAG_LABEL_BG = "rgba(198,40,40,0.85)";

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
// Shared helpers
// ---------------------------------------------------------------------------

/** Node sphere radius from node dimensions. */
function nodeRadius(node: RFNode<NodeData>): number {
  return Math.min((node.data?.width ?? 110), (node.data?.height ?? 60)) / 4;
}

function boundingBox(nodes: RFNode<NodeData>[]) {
  if (nodes.length === 0) return { minX: -200, maxX: 200, minY: -200, maxY: 200 };
  let minX = Infinity, maxX = -Infinity, minY = Infinity, maxY = -Infinity;
  for (const n of nodes) {
    const w = (n.data?.width ?? 110) / 2;
    const h = (n.data?.height ?? 60) / 2;
    minX = Math.min(minX, n.position.x - w);
    maxX = Math.max(maxX, n.position.x + w);
    minY = Math.min(minY, n.position.y - h);
    maxY = Math.max(maxY, n.position.y + h);
  }
  return { minX, maxX, minY, maxY };
}

/** World position for a node center (RF y-down → Three y-up). */
function nodeWorldPos(node: RFNode<NodeData>): THREE.Vector3 {
  const x = node.position.x + (node.data?.width ?? 110) / 2;
  const y = -(node.position.y + (node.data?.height ?? 60) / 2);
  return new THREE.Vector3(x, y, 0);
}

/**
 * Scene center (centroid of node bounding box), used for dolly distance.
 * Falls back to origin when no nodes.
 */
function sceneCenter(nodes: RFNode<NodeData>[]): THREE.Vector3 {
  if (nodes.length === 0) return new THREE.Vector3(0, 0, 0);
  const { minX, maxX, minY, maxY } = boundingBox(nodes);
  return new THREE.Vector3((minX + maxX) / 2, -(minY + maxY) / 2, 0);
}

/**
 * True perpendicular distance from camera to the z=0 plane (content plane).
 * This is the projection of the camera-to-origin vector onto the camera forward
 * direction, computed as: |cam.position · viewDir| where viewDir is the camera
 * forward in world space. Correct after arbitrary rotation (not just looking down -Z).
 * Clamped to a minimum of 10 to avoid zero/negative values.
 */
function camToPlaneDistance(cam: THREE.PerspectiveCamera): number {
  // Camera forward in world space (points away from camera, into scene).
  const forward = new THREE.Vector3(0, 0, -1).applyQuaternion(cam.quaternion);
  // Distance = projection of camera position onto the (negated) forward vector.
  // The z=0 plane has normal (0,0,1). Distance = |cam.position · (0,0,1)| = |cam.position.z|
  // when looking straight down. After rotation, use the component of the camera
  // position along the view axis (how far back the camera is from z=0 along view).
  // More precisely: distance from cam to the plane along the view ray.
  // Ray: origin=cam.position, dir=forward. Plane: z=0, normal=(0,0,1).
  // t = -cam.position.z / forward.z  (ray-plane intersection param)
  // If forward.z ≈ 0 (camera looking sideways), fall back to |cam.position.z|.
  const fwdZ = forward.z;
  if (Math.abs(fwdZ) > 0.01) {
    return Math.max(Math.abs(-cam.position.z / fwdZ), 10);
  }
  return Math.max(Math.abs(cam.position.z), 10);
}

/**
 * World-units-per-pixel for panning in the camera's screen plane.
 * Computed from the perpendicular distance to the content plane and the camera FOV.
 */
function worldPerPixel(cam: THREE.PerspectiveCamera, canvasH: number): number {
  const d = camToPlaneDistance(cam);
  const fovRad = (cam.fov * Math.PI) / 180;
  return (2 * d * Math.tan(fovRad / 2)) / canvasH;
}

// NDC ↔ pixel helpers
function ndcToPixel(ndcX: number, ndcY: number, size: { width: number; height: number }): { px: number; py: number } {
  const px = (ndcX + 1) / 2 * size.width;
  const py = (1 - (ndcY + 1) / 2) * size.height;
  return { px, py };
}

function pixelToNDC(clientX: number, clientY: number, rect: DOMRect): { ndcX: number; ndcY: number } {
  const ndcX = ((clientX - rect.left) / rect.width) * 2 - 1;
  const ndcY = -((clientY - rect.top) / rect.height) * 2 + 1;
  return { ndcX, ndcY };
}

// ---------------------------------------------------------------------------
// Camera fitter: perspective camera framed head-on to show graph flat at z=0.
// Fits once, but waits until nodes are actually non-empty (not just on mount).
// ---------------------------------------------------------------------------

function CameraFitter({ nodes }: { nodes: RFNode<NodeData>[] }) {
  const { camera, size } = useThree();
  const loadEpoch = useThreeStore((s) => s.loadEpoch);
  useEffect(() => {
    // Skip if no content or canvas not yet sized.
    if (nodes.length === 0) return;
    if (size.width === 0 || size.height === 0) return;
    const persp = camera as THREE.PerspectiveCamera;
    const PAD = 80;
    const { minX, maxX, minY, maxY } = boundingBox(nodes);
    const gw = (maxX - minX) + 2 * PAD;
    const gh = (maxY - minY) + 2 * PAD;
    const cx = (minX + maxX) / 2;
    const cy = -(minY + maxY) / 2; // RF y-down → negate
    const aspect = size.width / size.height;
    // Choose z so the graph fills the view.
    const fovRad = (persp.fov * Math.PI) / 180;
    const zForH = gh / 2 / Math.tan(fovRad / 2);
    const zForW = gw / 2 / aspect / Math.tan(fovRad / 2);
    const z = Math.max(zForH, zForW) + 50;
    persp.position.set(cx, cy, z);
    persp.lookAt(cx, cy, 0);
    persp.near = 0.1;
    persp.far = 20000;
    persp.updateProjectionMatrix();
  // Re-fit whenever a load epoch completes (loadSpec or loadView); skip on drag.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loadEpoch]);
  return null;
}

// ---------------------------------------------------------------------------
// Single node mesh: sphere + border ring
// ---------------------------------------------------------------------------

function GraphNode({
  node,
  selected,
  connectPending,
  hovered,
}: {
  node: RFNode<NodeData>;
  selected: boolean;
  connectPending: boolean;
  hovered: boolean;
}) {
  const pos = nodeWorldPos(node);
  const r = nodeRadius(node);
  const flagged = !!node.data?.validationError;

  const fillHex = flagged ? FLAG_FILL : (node.data?.fill ?? "#ffffff");
  const strokeHex = flagged ? FLAG_RING
    : connectPending ? "#00ffaa"
    : selected ? "#ffcc00"
    : hovered ? "#aaddff"
    : (node.data?.stroke ?? "#888888");

  // Memoize THREE.Color objects to avoid allocating on every render.
  const fillColor = useMemo(() => new THREE.Color(fillHex), [fillHex]);
  const strokeColor = useMemo(() => new THREE.Color(strokeHex), [strokeHex]);
  const emissiveFill = useMemo(
    () => flagged ? new THREE.Color(FLAG_FILL) : new THREE.Color(0x000000),
    [flagged],
  );
  const emissiveStroke = useMemo(
    () => flagged ? new THREE.Color(FLAG_RING) : new THREE.Color(0x000000),
    [flagged],
  );

  const torusThick = (selected || hovered || flagged) ? r * 0.14 : r * 0.08;

  return (
    <group position={[pos.x, pos.y, pos.z]}>
      <mesh>
        <sphereGeometry args={[r, 16, 16]} />
        <meshStandardMaterial
          color={fillColor}
          emissive={emissiveFill}
          emissiveIntensity={flagged ? 0.4 : 0}
        />
      </mesh>
      <mesh>
        <torusGeometry args={[r, torusThick, 8, 32]} />
        <meshStandardMaterial
          color={strokeColor}
          emissive={emissiveStroke}
          emissiveIntensity={flagged ? 0.5 : 0}
        />
      </mesh>
    </group>
  );
}

// ---------------------------------------------------------------------------
// Edges — 3D tube path using QuadraticBezierCurve3.
// Exit/entry points: point on each node's sphere surface facing the other node.
// ---------------------------------------------------------------------------

/** Point on the sphere surface of `node` facing toward `other`. */
function surfacePoint(node: RFNode<NodeData>, other: RFNode<NodeData>): THREE.Vector3 {
  const origin = nodeWorldPos(node);
  const target = nodeWorldPos(other);
  const r = nodeRadius(node);
  const dir = target.clone().sub(origin).normalize();
  return origin.clone().addScaledVector(dir, r);
}

// Speed constant matching the 2D pulse: PULSE_SPEED_PX_PER_MS = 0.08.
// In 3D we treat world units as equivalent to 2D pixels (same coordinate space).
const PULSE_SPEED_WU_PER_MS = 0.08;

// PulseBead: a bright sphere that travels along `curve` at the current pulse t.
// Driven by useFrame reading getPulseMap() imperatively (no React context needed).
function PulseBead({
  edgeId,
  curve,
  arcLength,
}: {
  edgeId: string;
  curve: THREE.QuadraticBezierCurve3;
  arcLength: number;
}) {
  const meshRef = useRef<THREE.Mesh>(null);

  useFrame(() => {
    const pulse = getPulseMap().get(edgeId);
    const mesh = meshRef.current;
    if (!mesh) return;
    if (!pulse) {
      mesh.visible = false;
      return;
    }
    const duration = arcLength / PULSE_SPEED_WU_PER_MS;
    const t = Math.min((getPauseAdjustedNow() - pulse.startTime) / duration, 1);
    if (t >= 1) {
      mesh.visible = false;
      if (claimDelivered(edgeId, pulse.startTime)) {
        vscode.postMessage({ type: "delivered", edge: edgeId });
      }
      return;
    }
    const pt = curve.getPoint(t);
    mesh.position.set(pt.x, pt.y, pt.z);
    mesh.visible = true;
  });

  return (
    <mesh ref={meshRef} visible={false}>
      <sphereGeometry args={[4, 8, 8]} />
      <meshStandardMaterial
        color="#ffffff"
        emissive={new THREE.Color(0xffffff)}
        emissiveIntensity={2.5}
      />
    </mesh>
  );
}

function SingleEdgeTube({ edgeId, src, tgt }: { edgeId: string; src: RFNode<NodeData>; tgt: RFNode<NodeData> }) {
  // Memoize geometry to avoid allocation every render — only rebuild when endpoints change.
  const p0key = `${src.id}:${tgt.id}:${src.position.x},${src.position.y},${tgt.position.x},${tgt.position.y}`;
  const { curve, arcLength, tubeGeo } = useMemo(() => {
    const _p0 = surfacePoint(src, tgt);
    const _p2 = surfacePoint(tgt, src);
    const mid = _p0.clone().add(_p2).multiplyScalar(0.5);
    const edgeDir = _p2.clone().sub(_p0).normalize();
    const lift = new THREE.Vector3(0, 0, 1).cross(edgeDir).normalize();
    const span = _p0.distanceTo(_p2);
    const _p1 = mid.clone().addScaledVector(lift, span * 0.25);
    const _curve = new THREE.QuadraticBezierCurve3(_p0, _p1, _p2);
    const _arcLength = _curve.getLength();
    const _tubeGeo = new THREE.TubeGeometry(_curve, 16, 1.5, 6, false);
    return { curve: _curve, arcLength: _arcLength, tubeGeo: _tubeGeo };
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [p0key]);

  return (
    <>
      {/* Always-lit base tube — emissive so it reads at any camera angle */}
      <mesh geometry={tubeGeo}>
        <meshStandardMaterial
          color="#5599cc"
          emissive={new THREE.Color(0x2255aa)}
          emissiveIntensity={0.8}
        />
      </mesh>
      {/* Pulse bead: stronger highlight traveling source → target */}
      <PulseBead edgeId={edgeId} curve={curve} arcLength={arcLength} />
    </>
  );
}

function GraphEdges({
  edges,
  nodeMap,
}: {
  edges: RFEdge<EdgeData>[];
  nodeMap: Map<string, RFNode<NodeData>>;
}) {
  return (
    <>
      {edges.map((e) => {
        const s = nodeMap.get(e.source);
        const t = nodeMap.get(e.target);
        if (!s || !t) return null;
        return <SingleEdgeTube key={e.id} edgeId={e.id} src={s} tgt={t} />;
      })}
    </>
  );
}

// ---------------------------------------------------------------------------
// CameraRef bridge: exposes the live camera to React state outside the Canvas.
// ---------------------------------------------------------------------------

function CameraRefBridge({
  cameraRef,
}: {
  cameraRef: React.MutableRefObject<THREE.PerspectiveCamera | null>;
}) {
  const { camera } = useThree();
  useEffect(() => {
    cameraRef.current = camera as THREE.PerspectiveCamera;
  }, [camera, cameraRef]);
  return null;
}

// ---------------------------------------------------------------------------
// LabelProjector: runs every N frames (throttled) to project node world
// positions to screen. Projects only the visible set (hovered ∪ selected ∪
// nearest-N) plus a full refresh every 6 frames for smooth camera motion.
// Updates positions via a ref callback (no React state → no re-render cost).
// ---------------------------------------------------------------------------

function LabelProjector({
  nodes,
  onPositions,
}: {
  nodes: RFNode<NodeData>[];
  onPositions: (positions: { id: string; px: number; py: number }[]) => void;
}) {
  const { camera, size } = useThree();
  const frameCountRef = useRef(0);

  useFrame(() => {
    frameCountRef.current++;
    // Project every 2 frames (~30fps) for label smoothness during camera motion.
    // This is much cheaper than every frame while still tracking well visually.
    if (frameCountRef.current % 2 !== 0) return;
    const positions = nodes.map((n) => {
      const world = nodeWorldPos(n);
      world.project(camera);
      const { px, py } = ndcToPixel(world.x, world.y, size);
      return { id: n.id, px, py };
    });
    onPositions(positions);
  });

  return null;
}

// ---------------------------------------------------------------------------
// CameraSettleDetector: fires onSettle ~250ms after the camera stops moving.
// Compares camera matrix each frame; on change resets a debounce timer.
// ---------------------------------------------------------------------------

function CameraSettleDetector({
  onSettle,
}: {
  onSettle: () => void;
}) {
  const { camera } = useThree();
  const lastMatrix = useRef<string>("");
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useFrame(() => {
    // Snapshot the camera matrix as a compact string (16 floats, 2 decimal places).
    camera.updateMatrixWorld();
    const key = camera.matrixWorld.elements.map((v) => v.toFixed(2)).join(",");
    if (key !== lastMatrix.current) {
      lastMatrix.current = key;
      if (timerRef.current !== null) clearTimeout(timerRef.current);
      timerRef.current = setTimeout(() => {
        timerRef.current = null;
        onSettle();
      }, 250);
    }
  });

  return null;
}

// ---------------------------------------------------------------------------
// computeOcclusionCounts: given projected positions + world positions, return
// a map from frontNodeId → count of nodes hidden directly behind it.
//
// Two nodes "overlap" if their projected screen-center distance < front node's
// projected radius. Front = nearer to camera. N = count of farther overlapping
// nodes behind the front one.
// ---------------------------------------------------------------------------

function computeOcclusionCounts(
  nodes: RFNode<NodeData>[],
  camera: THREE.Camera,
  size: { w: number; h: number },
): Map<string, number> {
  if (nodes.length < 2) return new Map();

  const wh = { width: size.w, height: size.h };

  // Project each node: screen px/py, camera distance, screen radius.
  type Proj = { id: string; px: number; py: number; dist: number; screenR: number };
  const projected: Proj[] = nodes.map((n) => {
    const worldCenter = nodeWorldPos(n);
    const dist = worldCenter.distanceTo(camera.position);

    const center = worldCenter.clone().project(camera);
    const { px, py } = ndcToPixel(center.x, center.y, wh);

    // Compute screen radius: project a point offset by the world node radius.
    const worldR = nodeRadius(n);
    const offsetWorld = worldCenter.clone().add(new THREE.Vector3(worldR, 0, 0));
    const offsetProj = offsetWorld.clone().project(camera);
    const offsetPx = ndcToPixel(offsetProj.x, offsetProj.y, wh).px;
    const screenR = Math.abs(offsetPx - px);

    return { id: n.id, px, py, dist, screenR };
  });

  const counts = new Map<string, number>();

  for (let i = 0; i < projected.length; i++) {
    for (let j = i + 1; j < projected.length; j++) {
      const a = projected[i];
      const b = projected[j];
      const dx = a.px - b.px;
      const dy = a.py - b.py;
      const screenDist = Math.sqrt(dx * dx + dy * dy);

      // Overlap: use front node's screen radius as threshold.
      const front = a.dist < b.dist ? a : b;

      if (screenDist < front.screenR) {
        counts.set(front.id, (counts.get(front.id) ?? 0) + 1);
      }
    }
  }

  return counts;
}

// ---------------------------------------------------------------------------
// RaycasterHelper: performs pick on demand via ref callback.
// ---------------------------------------------------------------------------

function RaycasterHelper({
  nodes,
  onPickRequest,
}: {
  nodes: RFNode<NodeData>[];
  onPickRequest: React.MutableRefObject<
    ((ndcX: number, ndcY: number) => string | null) | null
  >;
}) {
  const { camera, scene } = useThree();
  const raycaster = useRef(new THREE.Raycaster());

  useEffect(() => {
    onPickRequest.current = (ndcX: number, ndcY: number): string | null => {
      const ndc = new THREE.Vector2(ndcX, ndcY);
      raycaster.current.setFromCamera(ndc, camera);
      const meshes: THREE.Mesh[] = [];
      scene.traverse((obj) => {
        if ((obj as THREE.Mesh).isMesh) meshes.push(obj as THREE.Mesh);
      });
      const hits = raycaster.current.intersectObjects(meshes, false);
      if (hits.length === 0) return null;
      // Walk up to find a group that has a matching node position.
      const hitPoint = hits[0].object.parent;
      if (!hitPoint) return null;
      // Match the group position to a node world pos.
      for (const n of nodes) {
        const wp = nodeWorldPos(n);
        if (
          Math.abs(hitPoint.position.x - wp.x) < 1 &&
          Math.abs(hitPoint.position.y - wp.y) < 1
        ) {
          return n.id;
        }
      }
      return null;
    };
  }, [camera, scene, nodes]);

  return null;
}

// ---------------------------------------------------------------------------
// NearestNTracker: computes nearest-N nodes to camera each frame (throttled).
// Notifies via callback so the outer React tree can re-render label visibility.
// ---------------------------------------------------------------------------

function NearestNTracker({
  nodes,
  onNearestN,
}: {
  nodes: RFNode<NodeData>[];
  onNearestN: (ids: Set<string>) => void;
}) {
  const { camera } = useThree();
  const lastIds = useRef<string>("");
  // useRef so frameCount persists across renders without resetting.
  const frameCountRef = useRef(0);

  useFrame(() => {
    frameCountRef.current++;
    if (frameCountRef.current % 6 !== 0) return; // throttle: recompute ~10fps
    const sorted = nodes
      .map((n) => ({ id: n.id, dist: nodeWorldPos(n).distanceTo(camera.position) }))
      .sort((a, b) => a.dist - b.dist)
      .slice(0, NEAREST_N)
      .map((x) => x.id);
    const key = sorted.join(",");
    if (key !== lastIds.current) {
      lastIds.current = key;
      onNearestN(new Set(sorted));
    }
  });

  return null;
}

// ---------------------------------------------------------------------------
// Scene
// ---------------------------------------------------------------------------

function Scene({
  nodes,
  edges,
  selectedId,
  hoveredId,
  connectPendingId,
  cameraRef,
  onPickRequest,
  onPositions,
  onNearestN,
  onCameraSettle,
}: {
  nodes: RFNode<NodeData>[];
  edges: RFEdge<EdgeData>[];
  selectedId: string | null;
  hoveredId: string | null;
  connectPendingId: string | null;
  cameraRef: React.MutableRefObject<THREE.PerspectiveCamera | null>;
  onPickRequest: React.MutableRefObject<
    ((ndcX: number, ndcY: number) => string | null) | null
  >;
  onPositions: (positions: { id: string; px: number; py: number }[]) => void;
  onNearestN: (ids: Set<string>) => void;
  onCameraSettle: () => void;
}) {
  const nodeMap = new Map(nodes.map((n) => [n.id, n]));
  return (
    <>
      <CameraFitter nodes={nodes} />
      <CameraRefBridge cameraRef={cameraRef} />
      <RaycasterHelper nodes={nodes} onPickRequest={onPickRequest} />
      <LabelProjector nodes={nodes} onPositions={onPositions} />
      <NearestNTracker nodes={nodes} onNearestN={onNearestN} />
      <CameraSettleDetector onSettle={onCameraSettle} />
      <ambientLight intensity={0.6} />
      <directionalLight position={[0, 0, 10]} intensity={0.8} />
      {nodes.map((n) => (
        <GraphNode
          key={n.id}
          node={n}
          selected={n.id === selectedId}
          hovered={n.id === hoveredId}
          connectPending={n.id === connectPendingId}
        />
      ))}
      <GraphEdges edges={edges} nodeMap={nodeMap} />
    </>
  );
}

// ---------------------------------------------------------------------------
// useInteractionControls — the main interaction hook.
// Handles CLICK, DRAG (arcball), SCROLL (dolly), and DWELL detection.
// Returns event handlers to attach to the canvas wrapper div.
// ---------------------------------------------------------------------------

interface ControlState {
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

function useInteractionControls(
  cameraRef: React.MutableRefObject<THREE.PerspectiveCamera | null>,
  canvasSize: { w: number; h: number },
  pickRequest: React.MutableRefObject<((ndcX: number, ndcY: number) => string | null) | null>,
  onSelect: (id: string | null) => void,
  onPanPadActive: (pos: { x: number; y: number } | null) => void,
  connectPendingIdRef: React.MutableRefObject<string | null>,
  onConnectClick: (id: string | null) => void,
  nodesRef: React.MutableRefObject<RFNode<NodeData>[]>,
  onMoveNode: (id: string, x: number, y: number) => void,
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
    snapshotPushed: boolean;
  } | null>(null);

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
            snapshotPushed: false,
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
          // Node drag: push snapshot at drag start (before any position change).
          if (!nodeDragRef.current.snapshotPushed) {
            pushSnapshot();
            nodeDragRef.current.snapshotPushed = true;
          }
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
    [applyArcball, cameraRef, canvasSize, nodesRef, onMoveNode, onPanPadActive, pickPivot, unprojectToPlane],
  );

  const onPointerUp = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      const s = state.current;
      if (s.dwellTimer !== null) { clearTimeout(s.dwellTimer); s.dwellTimer = null; }
      onPanPadActive(null);

      // Node drag completed: persist position and suppress click/select.
      if (s.phase === "dragging" && nodeDragRef.current?.snapshotPushed) {
        const nd = nodeDragRef.current;
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
          // Connect mode: read through ref to avoid stale closure.
          // onConnectClick reads connectPendingIdRef.current (live value).
          const inConnectMode = connectPendingIdRef.current !== null;
          onConnectClick(hitId);
          if (!inConnectMode) {
            onSelect(hitId); // normal select when not in connect mode
          }
        }
      }

      s.phase = "idle";
    },
    [connectPendingIdRef, nodesRef, onConnectClick, onPanPadActive, onSelect, pickRequest],
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
    },
    [cameraRef, nodesRef],
  );

  return { onPointerDown, onPointerMove, onPointerUp, onWheel };
}

// ---------------------------------------------------------------------------
// Widgets: Roll slider, Dolly buttons, Pan pad
// ---------------------------------------------------------------------------

/** ROLL SLIDER: vertical slider (range -π..π) that rolls camera about its view axis. */
function RollSlider({ cameraRef }: { cameraRef: React.MutableRefObject<THREE.PerspectiveCamera | null> }) {
  const [rollDeg, setRollDeg] = useState(0);
  const prevRoll = useRef(0);

  const onChange = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
    const cam = cameraRef.current;
    if (!cam) return;
    const newDeg = parseFloat(e.target.value);
    const delta = newDeg - prevRoll.current;
    prevRoll.current = newDeg;
    setRollDeg(newDeg);

    // Roll camera about its forward (z) axis (local -z = forward; roll about it).
    const forward = new THREE.Vector3(0, 0, -1).applyQuaternion(cam.quaternion);
    const q = new THREE.Quaternion().setFromAxisAngle(forward, (delta * Math.PI) / 180);
    cam.quaternion.premultiply(q);
  }, [cameraRef]);

  return (
    <div
      style={{
        position: "absolute",
        right: 12,
        top: "50%",
        transform: "translateY(-50%)",
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        gap: 4,
        background: "rgba(0,0,0,0.55)",
        borderRadius: 8,
        padding: "8px 6px",
        pointerEvents: "auto",
        zIndex: 20,
        userSelect: "none",
      }}
    >
      <span style={{ color: "#aaa", fontSize: 9, fontFamily: "monospace" }}>ROLL</span>
      <input
        type="range"
        min={-180}
        max={180}
        step={1}
        value={rollDeg}
        onChange={onChange}
        style={{
          writingMode: "vertical-lr",
          direction: "rtl",
          width: 20,
          height: 120,
          cursor: "pointer",
          accentColor: "#4af",
        }}
      />
      <span style={{ color: "#aaa", fontSize: 9, fontFamily: "monospace" }}>{rollDeg}°</span>
    </div>
  );
}

/** DOLLY BUTTONS: hold ^/v to dolly in/out. Positive direction = toward scene (z decreases). */
function DollyButtons({
  cameraRef,
  nodesRef,
}: {
  cameraRef: React.MutableRefObject<THREE.PerspectiveCamera | null>;
  nodesRef: React.MutableRefObject<RFNode<NodeData>[]>;
}) {
  const frameRef = useRef<ReturnType<typeof requestAnimationFrame> | null>(null);

  const startDolly = useCallback((sign: number) => {
    const tick = () => {
      const cam = cameraRef.current;
      if (!cam) return;
      // sign = +1 → dolly toward scene (move camera in -z of its view)
      const dir = new THREE.Vector3(0, 0, -sign).applyQuaternion(cam.quaternion);
      // Use distance to scene center for correct speed after rotation.
      const center = sceneCenter(nodesRef.current);
      const dist = cam.position.distanceTo(center);
      const speed = 0.008 * Math.max(dist, 10);
      cam.position.addScaledVector(dir, speed);
      frameRef.current = requestAnimationFrame(tick);
    };
    frameRef.current = requestAnimationFrame(tick);
  }, [cameraRef, nodesRef]);

  const stopDolly = useCallback(() => {
    if (frameRef.current !== null) {
      cancelAnimationFrame(frameRef.current);
      frameRef.current = null;
    }
  }, []);

  const btnStyle: React.CSSProperties = {
    width: 32,
    height: 28,
    cursor: "pointer",
    background: "rgba(60,60,80,0.85)",
    border: "1px solid #555",
    borderRadius: 5,
    color: "#ddd",
    fontSize: 15,
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    userSelect: "none",
  };

  return (
    <div
      style={{
        position: "absolute",
        right: 12,
        bottom: 16,
        display: "flex",
        flexDirection: "column",
        gap: 4,
        pointerEvents: "auto",
        zIndex: 20,
      }}
    >
      {/* ^ = toward scene (dolly in) */}
      <div
        style={btnStyle}
        onMouseDown={() => startDolly(1)}
        onMouseUp={stopDolly}
        onMouseLeave={stopDolly}
        title="Dolly in"
      >▲</div>
      {/* v = away from scene (dolly out) */}
      <div
        style={btnStyle}
        onMouseDown={() => startDolly(-1)}
        onMouseUp={stopDolly}
        onMouseLeave={stopDolly}
        title="Dolly out"
      >▼</div>
    </div>
  );
}

/** PAN PAD: small floating pad that appears on dwell; drag to pan the camera. */
function PanPad({
  origin,
  cameraRef,
  canvasSize,
}: {
  origin: { x: number; y: number };
  cameraRef: React.MutableRefObject<THREE.PerspectiveCamera | null>;
  canvasSize: { w: number; h: number };
}) {
  const startPos = useRef({ x: 0, y: 0 });
  const camStartPos = useRef(new THREE.Vector3());
  const dragging = useRef(false);

  const onPadPointerDown = useCallback((e: React.PointerEvent) => {
    e.stopPropagation();
    dragging.current = true;
    startPos.current = { x: e.clientX, y: e.clientY };
    camStartPos.current = cameraRef.current?.position.clone() ?? new THREE.Vector3();
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
  }, [cameraRef]);

  const onPadPointerMove = useCallback((e: React.PointerEvent) => {
    if (!dragging.current) return;
    e.stopPropagation();
    const cam = cameraRef.current;
    if (!cam) return;
    const dx = e.clientX - startPos.current.x;
    const dy = e.clientY - startPos.current.y;
    // worldPerPixel uses true perpendicular distance — correct after rotation.
    const wpp = worldPerPixel(cam, canvasSize.h);
    const rightDir = new THREE.Vector3().setFromMatrixColumn(cam.matrixWorld, 0);
    const upDir = new THREE.Vector3().setFromMatrixColumn(cam.matrixWorld, 1);
    cam.position.copy(camStartPos.current)
      .addScaledVector(rightDir, -dx * wpp)
      .addScaledVector(upDir, dy * wpp);
  }, [cameraRef, canvasSize]);

  const onPadPointerUp = useCallback((e: React.PointerEvent) => {
    e.stopPropagation();
    dragging.current = false;
  }, []);

  const PAD_SIZE = 64;
  return (
    <div
      onPointerDown={onPadPointerDown}
      onPointerMove={onPadPointerMove}
      onPointerUp={onPadPointerUp}
      style={{
        position: "absolute",
        left: origin.x - PAD_SIZE / 2,
        top: origin.y - PAD_SIZE / 2,
        width: PAD_SIZE,
        height: PAD_SIZE,
        background: "rgba(80,120,200,0.35)",
        border: "1.5px solid rgba(100,160,255,0.7)",
        borderRadius: "50%",
        cursor: "grab",
        pointerEvents: "auto",
        zIndex: 30,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        color: "rgba(200,220,255,0.8)",
        fontSize: 10,
        fontFamily: "monospace",
        userSelect: "none",
      }}
    >
      PAN
    </div>
  );
}

// ---------------------------------------------------------------------------
// ThreeView: Canvas wrapper + interaction + label overlay + widgets
// ---------------------------------------------------------------------------

export function ThreeView() {
  const nodes = useThreeStore((s) => s.nodes);
  const edges = useThreeStore((s) => s.edges);
  const storeMoveNode = useThreeStore((s) => s.moveNode);
  const storeCreateEdge = useThreeStore((s) => s.createEdge);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [hoveredId, setHoveredId] = useState<string | null>(null);
  const [nearestNIds, setNearestNIds] = useState<Set<string>>(new Set());
  const [labelPositions, setLabelPositions] = useState<{ id: string; px: number; py: number }[]>([]);
  const [panPadOrigin, setPanPadOrigin] = useState<{ x: number; y: number } | null>(null);
  // Connect mode: first click = pending source node id; second click = create edge.
  const [connectPendingId, setConnectPendingId] = useState<string | null>(null);
  // Ref mirror of connectPendingId — read in onPointerUp to avoid stale closure.
  const connectPendingIdRef = useRef<string | null>(null);
  // Ref mirror of nodes — read in dolly/wheel to avoid stale closure.
  const nodesRef = useRef<RFNode<NodeData>[]>(nodes);

  const cameraRef = useRef<THREE.PerspectiveCamera | null>(null);
  const pickRequest = useRef<((ndcX: number, ndcY: number) => string | null) | null>(null);
  const containerRef = useRef<HTMLDivElement | null>(null);
  const [canvasSize, setCanvasSize] = useState({ w: 800, h: 600 });

  // Keep refs in sync with state.
  useEffect(() => {
    connectPendingIdRef.current = connectPendingId;
  }, [connectPendingId]);

  useEffect(() => {
    nodesRef.current = nodes;
  }, [nodes]);

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
  const pendingPositions = useRef<{ id: string; px: number; py: number }[]>([]);
  const onPositions = useCallback((positions: { id: string; px: number; py: number }[]) => {
    pendingPositions.current = positions;
    if (labelRaf.current === null) {
      labelRaf.current = requestAnimationFrame(() => {
        setLabelPositions(pendingPositions.current);
        labelRaf.current = null;
      });
    }
  }, []);

  // Connect-mode click handler: first click picks source, second click creates edge.
  // Clicking empty space (null) cancels.
  const onConnectClick = useCallback((hitId: string | null) => {
    // Read through ref for live value (called from onPointerUp which uses the ref).
    const pending = connectPendingIdRef.current;
    if (pending === null) {
      // Start connect mode only if a node was clicked
      if (hitId !== null) setConnectPendingId(hitId);
    } else {
      // We have a pending source
      if (hitId === null || hitId === pending) {
        // Cancel: empty click or re-click same node
        setConnectPendingId(null);
      } else {
        // Create the edge — auto-pick first output/input ports
        storeCreateEdge(pending, null, hitId, null);
        setConnectPendingId(null);
      }
    }
  }, [storeCreateEdge]); // storeCreateEdge is stable (zustand action)

  // Escape key cancels connect mode
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") { setConnectPendingId(null); return; }
      // Undo/redo: Cmd/Ctrl+Z, Cmd/Ctrl+Shift+Z (redo).
      const mod = e.metaKey || e.ctrlKey;
      if (mod && (e.key === "z" || e.key === "Z")) {
        e.preventDefault();
        if (e.shiftKey) redo(); else undo();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const { onPointerDown, onPointerMove, onPointerUp, onWheel } = useInteractionControls(
    cameraRef,
    canvasSize,
    pickRequest,
    setSelectedId,
    setPanPadOrigin,
    connectPendingIdRef,
    onConnectClick,
    nodesRef,
    storeMoveNode,
  );

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

  return (
    <div ref={containerRef} style={{ position: "absolute", inset: 0 }}>
      {/* Canvas + gesture capture layer */}
      <div
        style={{ position: "absolute", inset: 0, touchAction: "none" }}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMoveWithHover}
        onPointerUp={onPointerUp}
        onPointerLeave={onPointerLeave}
        onWheel={onWheel}
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
            connectPendingId={connectPendingId}
            cameraRef={cameraRef}
            onPickRequest={pickRequest}
            onPositions={onPositions}
            onNearestN={onNearestN}
            onCameraSettle={onCameraSettle}
          />
        </Canvas>
      </div>

      {/* Label overlay — real camera projection, updated every frame.
          LOD: show only hovered | selected | nearest-N nodes to avoid forest. */}
      {nodes.map((n) => {
        const pos = labelMap.get(n.id);
        if (!pos) return null;
        const isHovered = n.id === hoveredId;
        const isSelected = n.id === selectedId;
        const isNearest = nearestNIds.has(n.id);
        if (!isHovered && !isSelected && !isNearest) return null;
        const flagged = !!n.data?.validationError;
        return (
          <div
            key={n.id}
            style={{
              position: "absolute",
              left: pos.px,
              top: pos.py + 4,
              transform: "translateX(-50%)",
              fontSize: 11,
              fontFamily: "monospace",
              color: flagged ? "#fff" : "#e0e0e0",
              background: flagged ? FLAG_LABEL_BG : "transparent",
              border: flagged ? "1px solid #ff5252" : "none",
              borderRadius: flagged ? 3 : 0,
              padding: flagged ? "1px 4px" : 0,
              textShadow: flagged ? "none" : "0 0 3px #000",
              pointerEvents: "none",
              whiteSpace: "nowrap",
              zIndex: 10,
            }}
          >
            {n.data?.label ?? n.id}
            {n.data?.sublabel ? (
              <span style={{ opacity: 0.7 }}> · {n.data.sublabel}</span>
            ) : null}
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

      {/* Connect-mode hint banner */}
      {connectPendingId !== null && (
        <div style={{
          position: "absolute",
          top: 10,
          left: "50%",
          transform: "translateX(-50%)",
          background: "rgba(0,200,120,0.85)",
          color: "#000",
          fontSize: 11,
          fontFamily: "monospace",
          padding: "4px 12px",
          borderRadius: 5,
          pointerEvents: "none",
          zIndex: 40,
          whiteSpace: "nowrap",
        }}>
          Click target node to wire · Esc to cancel
        </div>
      )}

      {/* Widgets — fixed corner, pointerEvents auto */}
      <RollSlider cameraRef={cameraRef} />
      <DollyButtons cameraRef={cameraRef} nodesRef={nodesRef} />

      {/* Pan pad — shown on dwell */}
      {panPadOrigin && (
        <PanPad
          origin={panPadOrigin}
          cameraRef={cameraRef}
          canvasSize={canvasSize}
        />
      )}
    </div>
  );
}
