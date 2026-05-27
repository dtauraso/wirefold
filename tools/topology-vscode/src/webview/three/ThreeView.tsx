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
import {
  nodeRadius,
  boundingBox,
  nodeWorldPos,
  sceneCenter,
  worldPerPixel,
  ndcToPixel,
  pixelToNDC,
} from "./geometry-helpers";
import { RollSlider, DollyButtons, PanPad } from "./camera-ui";
import { useInteractionControls } from "./interaction-controls";
import type { PickOptions } from "./interaction-controls";

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
  hovered,
  faded,
}: {
  node: RFNode<NodeData>;
  selected: boolean;
  hovered: boolean;
  faded: boolean;
}) {
  const pos = nodeWorldPos(node);
  const r = nodeRadius(node);
  const flagged = !!node.data?.validationError;

  const fillHex = flagged ? FLAG_FILL : (node.data?.fill ?? "#ffffff");
  const strokeHex = flagged ? FLAG_RING
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
  const fadeOpacity = 0.25;

  return (
    <group position={[pos.x, pos.y, pos.z]}>
      <mesh>
        <sphereGeometry args={[r, 16, 16]} />
        <meshStandardMaterial
          key={faded ? "faded" : "solid"}
          color={fillColor}
          emissive={emissiveFill}
          emissiveIntensity={flagged ? 0.4 : 0}
          transparent={faded}
          opacity={faded ? fadeOpacity : 1}
        />
      </mesh>
      <mesh>
        <torusGeometry args={[r, torusThick, 8, 32]} />
        <meshStandardMaterial
          key={faded ? "faded" : "solid"}
          color={strokeColor}
          emissive={emissiveStroke}
          emissiveIntensity={flagged ? 0.5 : 0}
          transparent={faded}
          opacity={faded ? fadeOpacity : 1}
        />
      </mesh>
      <mesh>
        <sphereGeometry args={[r * 1.45, 16, 16]} />
        <meshBasicMaterial
          color="#ff5a00"
          transparent
          opacity={selected ? 0.5 : 0}
          side={THREE.DoubleSide}
          depthWrite={false}
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
    const pt = curve.getPointAt(t);
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

function SingleEdgeTube({ edgeId, src, tgt, faded, selected }: { edgeId: string; src: RFNode<NodeData>; tgt: RFNode<NodeData>; faded: boolean; selected: boolean }) {
  // Memoize geometry to avoid allocation every render — only rebuild when endpoints change.
  const p0key = `${src.id}:${tgt.id}:${src.position.x},${src.position.y},${tgt.position.x},${tgt.position.y}`;
  const { curve, arcLength, tubeGeo, haloGeo } = useMemo(() => {
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
    // Halo: concentric tube on the same curve, larger radius — reads as a glow around the core.
    const _haloGeo = new THREE.TubeGeometry(_curve, 16, 5, 6, false);
    return { curve: _curve, arcLength: _arcLength, tubeGeo: _tubeGeo, haloGeo: _haloGeo };
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [p0key]);

  return (
    <>
      {/* Always-lit base tube — emissive so it reads at any camera angle */}
      <mesh geometry={tubeGeo} userData={{ edgeId }}>
        <meshStandardMaterial
          key={faded ? "faded" : "solid"}
          color="#5599cc"
          emissive={new THREE.Color(0x2255aa)}
          emissiveIntensity={0.8}
          transparent={faded}
          opacity={faded ? 0.25 : 1}
        />
      </mesh>
      {/* Selection halo doubles as the wide pick target. Always mounted so the raycaster
          can hit anywhere within the halo radius; painted only when selected (opacity 0
          otherwise — an opacity-0 visible mesh is still raycast-hittable). */}
      <mesh geometry={haloGeo} userData={{ edgeId }}>
        <meshBasicMaterial
          color="#ff5a00"
          transparent
          opacity={selected ? 0.6 : 0}
          side={THREE.DoubleSide}
          depthWrite={false}
        />
      </mesh>
      {/* Pulse bead: stronger highlight traveling source → target */}
      {!faded && <PulseBead edgeId={edgeId} curve={curve} arcLength={arcLength} />}
    </>
  );
}

function GraphEdges({
  edges,
  nodeMap,
  selectedId,
}: {
  edges: RFEdge<EdgeData>[];
  nodeMap: Map<string, RFNode<NodeData>>;
  selectedId: string | null;
}) {
  return (
    <>
      {edges.map((e) => {
        const s = nodeMap.get(e.source);
        const t = nodeMap.get(e.target);
        if (!s || !t) return null;
        return <SingleEdgeTube key={e.id} edgeId={e.id} src={s} tgt={t} faded={!!e.data?.faded} selected={e.id === selectedId} />;
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
    ((ndcX: number, ndcY: number, opts?: PickOptions) => string | null) | null
  >;
}) {
  const { camera, scene } = useThree();
  const raycaster = useRef(new THREE.Raycaster());

  useEffect(() => {
    onPickRequest.current = (ndcX: number, ndcY: number, opts?: PickOptions): string | null => {
      const ndc = new THREE.Vector2(ndcX, ndcY);
      raycaster.current.setFromCamera(ndc, camera);
      const meshes: THREE.Mesh[] = [];
      scene.traverse((obj) => {
        if ((obj as THREE.Mesh).isMesh) meshes.push(obj as THREE.Mesh);
      });
      const hits = raycaster.current.intersectObjects(meshes, false);
      if (hits.length === 0) return null;

      if (opts?.nodesOnly) {
        // Iterate all hits to find the first node that isn't excluded.
        for (const hit of hits) {
          const hitObj = hit.object as THREE.Mesh;
          if (hitObj.userData?.edgeId) continue; // skip edges
          const hitPoint = hitObj.parent;
          if (!hitPoint) continue;
          for (const n of nodes) {
            if (opts.excludeId && n.id === opts.excludeId) continue;
            const wp = nodeWorldPos(n);
            if (
              Math.abs(hitPoint.position.x - wp.x) < 1 &&
              Math.abs(hitPoint.position.y - wp.y) < 1
            ) {
              return n.id;
            }
          }
        }
        return null;
      }

      // Check if the hit mesh is an edge tube (carries userData.edgeId).
      const hitObj = hits[0].object as THREE.Mesh;
      if (hitObj.userData?.edgeId) return hitObj.userData.edgeId as string;
      // Walk up to find a group that has a matching node position.
      const hitPoint = hitObj.parent;
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
  cameraRef: React.MutableRefObject<THREE.PerspectiveCamera | null>;
  onPickRequest: React.MutableRefObject<
    ((ndcX: number, ndcY: number, opts?: PickOptions) => string | null) | null
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
          faded={!!n.data?.faded}
        />
      ))}
      <GraphEdges edges={edges} nodeMap={nodeMap} selectedId={selectedId} />
    </>
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
  const toggleFade = useThreeStore((s) => s.toggleFade);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [hoveredId, setHoveredId] = useState<string | null>(null);
  const [nearestNIds, setNearestNIds] = useState<Set<string>>(new Set());
  const [labelPositions, setLabelPositions] = useState<{ id: string; px: number; py: number }[]>([]);
  const [panPadOrigin, setPanPadOrigin] = useState<{ x: number; y: number } | null>(null);
  // Ref mirror of nodes — read in dolly/wheel to avoid stale closure.
  const nodesRef = useRef<RFNode<NodeData>[]>(nodes);

  const cameraRef = useRef<THREE.PerspectiveCamera | null>(null);
  const pickRequest = useRef<((ndcX: number, ndcY: number, opts?: PickOptions) => string | null) | null>(null);
  const containerRef = useRef<HTMLDivElement | null>(null);
  const [canvasSize, setCanvasSize] = useState({ w: 800, h: 600 });

  // Keep refs in sync with state.
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

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const mod = e.metaKey || e.ctrlKey;
      // "f": toggle fade on the selected element.
      if (e.key === "f" && !mod && selectedId) {
        const isEdge = edges.some((ed) => ed.id === selectedId);
        toggleFade({ kind: isEdge ? "edge" : "node", id: selectedId });
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [selectedId, edges, toggleFade]);

  const { onPointerDown, onPointerMove, onPointerUp, onWheel } = useInteractionControls(
    cameraRef,
    canvasSize,
    pickRequest,
    setSelectedId,
    setPanPadOrigin,
    nodesRef,
    storeMoveNode,
    storeCreateEdge,
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
