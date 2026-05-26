// ThreeView — 3D replica of the RF graph with interaction grammar:
//   - PerspectiveCamera (parallax + depth cues)
//   - Click → raycast pick/select
//   - Drag → arcball rotation (pivot at picked 3D point or scene center)
//   - Scroll → dolly along view axis
//   - Roll slider widget (screen-plane camera roll)
//   - Dolly buttons (hold-to-dolly)
//   - Pan pad (200ms dwell over empty canvas → draggable pad)

import { useEffect, useRef, useState, useCallback } from "react";
import { Canvas, useThree, useFrame } from "@react-three/fiber";
import * as THREE from "three";
import type { Node as RFNode, Edge as RFEdge } from "reactflow";
import { rfGetNodes, rfGetEdges, subscribeRFState, rfCreateEdge } from "../rf-imperative";
import type { NodeData, EdgeData } from "../types";

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
// Helpers
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Camera fitter: perspective camera framed head-on to show graph flat at z=0.
// ---------------------------------------------------------------------------

function CameraFitter({ nodes }: { nodes: RFNode<NodeData>[] }) {
  const { camera, size } = useThree();
  const fitted = useRef(false);
  useEffect(() => {
    if (fitted.current) return; // only auto-fit once on mount
    fitted.current = true;
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
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []); // intentionally empty deps — fit once on first mount
  return null;
}

// ---------------------------------------------------------------------------
// Single node mesh: sphere + border ring
// ---------------------------------------------------------------------------

function GraphNode({
  node,
  selected,
  connectPending,
}: {
  node: RFNode<NodeData>;
  selected: boolean;
  connectPending: boolean;
}) {
  const pos = nodeWorldPos(node);
  const r = Math.min((node.data?.width ?? 110), (node.data?.height ?? 60)) / 4;
  const fillHex = node.data?.fill ?? "#ffffff";
  const strokeHex = connectPending ? "#00ffaa"
    : selected ? "#ffcc00"
    : (node.data?.stroke ?? "#888888");
  const fillColor = new THREE.Color(fillHex);
  const strokeColor = new THREE.Color(strokeHex);

  return (
    <group position={[pos.x, pos.y, pos.z]}>
      <mesh>
        <sphereGeometry args={[r, 16, 16]} />
        <meshStandardMaterial color={fillColor} />
      </mesh>
      <mesh>
        <torusGeometry args={[r, selected ? r * 0.14 : r * 0.08, 8, 32]} />
        <meshStandardMaterial color={strokeColor} />
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
  const r = Math.min((node.data?.width ?? 110), (node.data?.height ?? 60)) / 4;
  const dir = target.clone().sub(origin).normalize();
  return origin.clone().addScaledVector(dir, r);
}

function SingleEdgeTube({ src, tgt }: { src: RFNode<NodeData>; tgt: RFNode<NodeData> }) {
  const p0 = surfacePoint(src, tgt);
  const p2 = surfacePoint(tgt, src);
  // Control point: midpoint lifted along the cross product of the edge and Z axis,
  // giving a gentle curve out of the plane.
  const mid = p0.clone().add(p2).multiplyScalar(0.5);
  const edgeDir = p2.clone().sub(p0).normalize();
  const lift = new THREE.Vector3(0, 0, 1).cross(edgeDir).normalize();
  const span = p0.distanceTo(p2);
  const p1 = mid.clone().addScaledVector(lift, span * 0.25);

  const curve = new THREE.QuadraticBezierCurve3(p0, p1, p2);
  const tubeGeo = new THREE.TubeGeometry(curve, 16, 1.5, 6, false);
  return (
    <mesh geometry={tubeGeo}>
      <meshStandardMaterial color="#888888" />
    </mesh>
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
        return <SingleEdgeTube key={e.id} src={s} tgt={t} />;
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
// LabelProjector: runs on every frame to project node world positions to screen.
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

  useFrame(() => {
    const positions = nodes.map((n) => {
      const world = nodeWorldPos(n);
      world.project(camera);
      // NDC → pixels (y flipped: NDC +1 = top)
      const px = (world.x + 1) / 2 * size.width;
      const py = (1 - (world.y + 1) / 2) * size.height;
      return { id: n.id, px, py };
    });
    onPositions(positions);
  });

  return null;
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
// Scene
// ---------------------------------------------------------------------------

function Scene({
  nodes,
  edges,
  selectedId,
  connectPendingId,
  cameraRef,
  onPickRequest,
  onPositions,
}: {
  nodes: RFNode<NodeData>[];
  edges: RFEdge<EdgeData>[];
  selectedId: string | null;
  connectPendingId: string | null;
  cameraRef: React.MutableRefObject<THREE.PerspectiveCamera | null>;
  onPickRequest: React.MutableRefObject<
    ((ndcX: number, ndcY: number) => string | null) | null
  >;
  onPositions: (positions: { id: string; px: number; py: number }[]) => void;
}) {
  const nodeMap = new Map(nodes.map((n) => [n.id, n]));
  return (
    <>
      <CameraFitter nodes={nodes} />
      <CameraRefBridge cameraRef={cameraRef} />
      <RaycasterHelper nodes={nodes} onPickRequest={onPickRequest} />
      <LabelProjector nodes={nodes} onPositions={onPositions} />
      <ambientLight intensity={0.6} />
      <directionalLight position={[0, 0, 10]} intensity={0.8} />
      {nodes.map((n) => (
        <GraphNode key={n.id} node={n} selected={n.id === selectedId} connectPending={n.id === connectPendingId} />
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
  connectPendingId: string | null,
  onConnectClick: (id: string | null) => void,
) {
  const state = useRef<ControlState>({
    phase: "idle",
    downX: 0, downY: 0, downTime: 0,
    prevX: 0, prevY: 0,
    dwellTimer: null,
  });

  // Pivot for the current drag (world space)
  const dragPivot = useRef(new THREE.Vector3());
  // Pan-pad origin for panning
  const panPadOrigin = useRef({ x: 0, y: 0 });
  // Camera state at pan-pad activation
  const panStartCamPos = useRef(new THREE.Vector3());

  // ------ helpers ------

  const getNDC = useCallback((clientX: number, clientY: number, rect: DOMRect) => {
    return {
      ndcX: ((clientX - rect.left) / rect.width) * 2 - 1,
      ndcY: -((clientY - rect.top) / rect.height) * 2 + 1,
    };
  }, []);

  /** Pick the 3D point under the cursor (raycasts; falls back to scene center). */
  const pickPivot = useCallback(
    (clientX: number, clientY: number, rect: DOMRect): THREE.Vector3 => {
      const cam = cameraRef.current;
      if (!cam) return new THREE.Vector3();
      const { ndcX, ndcY } = getNDC(clientX, clientY, rect);
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
    [cameraRef, getNDC, pickRequest],
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

      // Check if pointer is over empty space: start dwell timer.
      const rect = (e.currentTarget as HTMLDivElement).getBoundingClientRect();
      const { ndcX, ndcY } = getNDC(e.clientX, e.clientY, rect);
      const hitId = pickRequest.current?.(ndcX, ndcY) ?? null;

      if (hitId === null) {
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
    [cameraRef, getNDC, onPanPadActive, pickRequest],
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
        // Determine pivot.
        const rect = (e.currentTarget as HTMLDivElement).getBoundingClientRect();
        dragPivot.current = pickPivot(s.downX, s.downY, rect);
      }

      if (s.phase === "dragging") {
        const ddx = e.clientX - s.prevX;
        const ddy = e.clientY - s.prevY;
        applyArcball(ddx, ddy, dragPivot.current);
        s.prevX = e.clientX;
        s.prevY = e.clientY;
      }

      if (s.phase === "panning") {
        const cam = cameraRef.current;
        if (!cam) return;
        // Pan camera in its local XY plane proportional to pointer delta.
        const panDx = (e.clientX - panPadOrigin.current.x);
        const panDy = (e.clientY - panPadOrigin.current.y);
        // Scale pan to world units: use distance from camera to z=0 plane.
        const dist2origin = Math.abs(cam.position.z);
        const fovRad = (cam.fov * Math.PI) / 180;
        const worldPerPx = (2 * dist2origin * Math.tan(fovRad / 2)) / canvasSize.h;
        const rightDir = new THREE.Vector3().setFromMatrixColumn(cam.matrixWorld, 0);
        const upDir = new THREE.Vector3().setFromMatrixColumn(cam.matrixWorld, 1);
        cam.position.copy(panStartCamPos.current)
          .addScaledVector(rightDir, -panDx * worldPerPx)
          .addScaledVector(upDir, panDy * worldPerPx);
      }
    },
    [applyArcball, cameraRef, canvasSize, onPanPadActive, pickPivot],
  );

  const onPointerUp = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      const s = state.current;
      if (s.dwellTimer !== null) { clearTimeout(s.dwellTimer); s.dwellTimer = null; }
      onPanPadActive(null);

      if (s.phase === "pending") {
        const elapsed = Date.now() - s.downTime;
        const ddx = e.clientX - s.downX;
        const ddy = e.clientY - s.downY;
        const dist = Math.sqrt(ddx * ddx + ddy * ddy);
        if (elapsed < CLICK_MAX_MS && dist < MOVE_SLOP_PX) {
          // CLICK → pick
          const rect = (e.currentTarget as HTMLDivElement).getBoundingClientRect();
          const { ndcX, ndcY } = getNDC(e.clientX, e.clientY, rect);
          const hitId = pickRequest.current?.(ndcX, ndcY) ?? null;
          // Connect mode: route click to connect handler; else normal select
          onConnectClick(hitId);
          if (connectPendingId === null) {
            onSelect(hitId); // normal select when not in connect mode
          }
        }
      }

      s.phase = "idle";
    },
    [connectPendingId, getNDC, onConnectClick, onPanPadActive, onSelect, pickRequest],
  );

  const onWheel = useCallback(
    (e: React.WheelEvent<HTMLDivElement>) => {
      const cam = cameraRef.current;
      if (!cam) return;
      // Dolly along view direction. Positive deltaY = scroll-down = zoom out.
      const dir = new THREE.Vector3(0, 0, 1).applyQuaternion(cam.quaternion);
      const speed = 0.001 * Math.abs(cam.position.z || 500);
      cam.position.addScaledVector(dir, e.deltaY * speed);
    },
    [cameraRef],
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
function DollyButtons({ cameraRef }: { cameraRef: React.MutableRefObject<THREE.PerspectiveCamera | null> }) {
  const frameRef = useRef<ReturnType<typeof requestAnimationFrame> | null>(null);

  const startDolly = useCallback((sign: number) => {
    const tick = () => {
      const cam = cameraRef.current;
      if (!cam) return;
      // sign = +1 → dolly toward scene (move camera in -z of its view)
      const dir = new THREE.Vector3(0, 0, -sign).applyQuaternion(cam.quaternion);
      const speed = 0.008 * Math.abs(cam.position.z || 500);
      cam.position.addScaledVector(dir, speed);
      frameRef.current = requestAnimationFrame(tick);
    };
    frameRef.current = requestAnimationFrame(tick);
  }, [cameraRef]);

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
    const dist2origin = Math.abs(cam.position.z);
    const fovRad = (cam.fov * Math.PI) / 180;
    const worldPerPx = (2 * dist2origin * Math.tan(fovRad / 2)) / canvasSize.h;
    const rightDir = new THREE.Vector3().setFromMatrixColumn(cam.matrixWorld, 0);
    const upDir = new THREE.Vector3().setFromMatrixColumn(cam.matrixWorld, 1);
    cam.position.copy(camStartPos.current)
      .addScaledVector(rightDir, -dx * worldPerPx)
      .addScaledVector(upDir, dy * worldPerPx);
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
  const [nodes, setNodes] = useState<RFNode<NodeData>[]>(() => rfGetNodes() as RFNode<NodeData>[]);
  const [edges, setEdges] = useState<RFEdge<EdgeData>[]>(() => rfGetEdges() as RFEdge<EdgeData>[]);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [labelPositions, setLabelPositions] = useState<{ id: string; px: number; py: number }[]>([]);
  const [panPadOrigin, setPanPadOrigin] = useState<{ x: number; y: number } | null>(null);
  // Connect mode: first click = pending source node id; second click = create edge.
  const [connectPendingId, setConnectPendingId] = useState<string | null>(null);

  const cameraRef = useRef<THREE.PerspectiveCamera | null>(null);
  const pickRequest = useRef<((ndcX: number, ndcY: number) => string | null) | null>(null);
  const containerRef = useRef<HTMLDivElement | null>(null);
  const [canvasSize, setCanvasSize] = useState({ w: 800, h: 600 });

  // RF state subscription
  useEffect(() => {
    return subscribeRFState((ns, es) => {
      setNodes(ns as RFNode<NodeData>[]);
      setEdges(es as RFEdge<EdgeData>[]);
    });
  }, []);

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
    if (connectPendingId === null) {
      // Start connect mode only if a node was clicked
      if (hitId !== null) setConnectPendingId(hitId);
    } else {
      // We have a pending source
      if (hitId === null || hitId === connectPendingId) {
        // Cancel: empty click or re-click same node
        setConnectPendingId(null);
      } else {
        // Create the edge — auto-pick first output/input ports
        rfCreateEdge(connectPendingId, null, hitId, null);
        setConnectPendingId(null);
      }
    }
  }, [connectPendingId]);

  // Escape key cancels connect mode
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setConnectPendingId(null);
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
    connectPendingId,
    onConnectClick,
  );

  const labelMap = new Map(labelPositions.map((p) => [p.id, p]));

  return (
    <div ref={containerRef} style={{ position: "absolute", inset: 0 }}>
      {/* Canvas + gesture capture layer */}
      <div
        style={{ position: "absolute", inset: 0, touchAction: "none" }}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
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
            connectPendingId={connectPendingId}
            cameraRef={cameraRef}
            onPickRequest={pickRequest}
            onPositions={onPositions}
          />
        </Canvas>
      </div>

      {/* Label overlay — real camera projection, updated every frame */}
      {nodes.map((n) => {
        const pos = labelMap.get(n.id);
        if (!pos) return null;
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
              color: "#e0e0e0",
              textShadow: "0 0 3px #000",
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
      <DollyButtons cameraRef={cameraRef} />

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
