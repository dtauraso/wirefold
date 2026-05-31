// scene-content.tsx — 3D scene render components for ThreeView.
// CameraFitter, GraphNode, PulseBead, SingleEdgeTube, GraphEdges,
// CameraRefBridge, LabelProjector, CameraSettleDetector,
// computeOcclusionCounts, RaycasterHelper, NearestNTracker, Scene.

import { useEffect, useRef, useMemo } from "react";
import { useThree, useFrame } from "@react-three/fiber";
import * as THREE from "three";
import type { RFNode, RFEdge, NodeData, EdgeData } from "../types";
import type { Camera3D } from "../state/viewer/types";
import { useThreeStore } from "./store";
import { getPulseMap, claimDelivered, getCurve } from "./pulse-state";
import { vscode } from "../vscode-api";
import { getPauseAdjustedNow } from "../state/run-status";
import {
  nodeRadius,
  boundingBox,
  nodeWorldPos,
  nodeTopWorldPos,
  ndcToPixel,
} from "./geometry-helpers";
import { CURVE_PARAM_BULGE_FACTOR } from "../../schema/curve-params";
import type { PickOptions } from "./interaction-controls";

// ---------------------------------------------------------------------------
// Label LOD constants
// ---------------------------------------------------------------------------

/** Show label for the N nodes nearest to the camera, in addition to hovered/selected. */
export const NEAREST_N = 8;

/** Validation flag colors. */
export const FLAG_FILL = "#c62828";
export const FLAG_RING = "#ff5252";
export const FLAG_LABEL_BG = "rgba(198,40,40,0.85)";

// ---------------------------------------------------------------------------
// Camera fitter: perspective camera framed head-on to show graph flat at z=0.
// Fits once, but waits until nodes are actually non-empty (not just on mount).
// ---------------------------------------------------------------------------

export function CameraFitter({ nodes, hasRestoredCamera }: { nodes: RFNode<NodeData>[]; hasRestoredCamera?: boolean }) {
  const { camera, size } = useThree();
  const loadEpoch = useThreeStore((s) => s.loadEpoch);
  useEffect(() => {
    // Skip if no content or canvas not yet sized.
    if (nodes.length === 0) return;
    if (size.width === 0 || size.height === 0) return;
    // Skip auto-fit when the saved camera is being restored.
    if (hasRestoredCamera) return;
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
    persp.up.set(0, 1, 0);
    persp.lookAt(cx, cy, 0);
    persp.near = 0.1;
    persp.far = 20000;
    persp.updateProjectionMatrix();
  // Re-fit whenever a load epoch completes; skip on drag.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loadEpoch]);
  return null;
}

// ---------------------------------------------------------------------------
// Single node mesh: sphere + border ring
// ---------------------------------------------------------------------------

export function GraphNode({
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

// PulseBead: a bright sphere that travels along the edge curve at the current pulse t.
// Duration is substrate-supplied (pulse.simLatencyMs); no speed constant needed.
// Driven by useFrame reading getPulseMap() and getCurve() imperatively (no React
// context needed). The curve is read from the non-React curve store so it updates
// atomically with position changes in the same drag tick (no React-commit lag).
export function PulseBead({
  edgeId,
}: {
  edgeId: string;
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
    const curve = getCurve(edgeId);
    if (!curve) {
      mesh.visible = false;
      return;
    }
    const duration = pulse.simLatencyMs;
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

export function SingleEdgeTube({ edgeId, src, tgt, faded, selected }: { edgeId: string; src: RFNode<NodeData>; tgt: RFNode<NodeData>; faded: boolean; selected: boolean }) {
  // Memoize geometry to avoid allocation every render — only rebuild when endpoints change.
  const p0key = `${src.id}:${tgt.id}:${src.position.x},${src.position.y},${tgt.position.x},${tgt.position.y}`;
  const { tubeGeo, haloGeo } = useMemo(() => {
    // Build tube geometry from the curve store (already populated synchronously
    // by moveNode / load / createEdge). Fall back to constructing locally if the
    // store entry is somehow absent (e.g. on first mount before load completes).
    const _curve = getCurve(edgeId) ?? (() => {
      const _p0 = surfacePoint(src, tgt);
      const _p2 = surfacePoint(tgt, src);
      const mid = _p0.clone().add(_p2).multiplyScalar(0.5);
      const edgeDir = _p2.clone().sub(_p0).normalize();
      const lift = new THREE.Vector3(0, 0, 1).cross(edgeDir).normalize();
      const span = _p0.distanceTo(_p2);
      const _p1 = mid.clone().addScaledVector(lift, span * CURVE_PARAM_BULGE_FACTOR);
      return new THREE.QuadraticBezierCurve3(_p0, _p1, _p2);
    })();
    const _tubeGeo = new THREE.TubeGeometry(_curve, 16, 1.5, 6, false);
    // Halo: concentric tube on the same curve, larger radius — reads as a glow around the core.
    const _haloGeo = new THREE.TubeGeometry(_curve, 16, 5, 6, false);
    return { tubeGeo: _tubeGeo, haloGeo: _haloGeo };
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
      {!faded && <PulseBead edgeId={edgeId} />}
    </>
  );
}

export function GraphEdges({
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

export function CameraRefBridge({
  cameraRef,
  initialCamera3d,
}: {
  cameraRef: React.MutableRefObject<THREE.PerspectiveCamera | null>;
  initialCamera3d?: Camera3D;
}) {
  const { camera } = useThree();
  useEffect(() => {
    const cam = camera as THREE.PerspectiveCamera;
    cameraRef.current = cam;
    // Restore saved position but NEVER apply a saved quaternion — the camera is
    // permanently locked square-on (looking straight down -z toward the z=0 plane).
    // NOTE: initialCamera3d is a dependency so that if load() populates
    // viewerState.camera3d after the first mount (which happens asynchronously
    // on reload), the effect re-runs and the saved position is applied.
    if (initialCamera3d) {
      cam.position.set(...initialCamera3d.position);
    }
    cam.up.set(0, 1, 0);
    cam.lookAt(cam.position.x, cam.position.y, 0);
    cam.updateMatrixWorld(true);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [camera, cameraRef, initialCamera3d]);
  return null;
}

// ---------------------------------------------------------------------------
// LabelProjector: runs every N frames (throttled) to project node world
// positions to screen. Projects only the visible set (hovered ∪ selected ∪
// nearest-N) plus a full refresh every 6 frames for smooth camera motion.
// Updates positions via a ref callback (no React state → no re-render cost).
// ---------------------------------------------------------------------------

export function LabelProjector({
  nodes,
  onPositions,
}: {
  nodes: RFNode<NodeData>[];
  onPositions: (positions: { id: string; px: number; py: number; cx: number; cy: number }[]) => void;
}) {
  const { camera, size } = useThree();
  const frameCountRef = useRef(0);

  useFrame(() => {
    frameCountRef.current++;
    // Project every 2 frames (~30fps) for label smoothness during camera motion.
    // This is much cheaper than every frame while still tracking well visually.
    if (frameCountRef.current % 2 !== 0) return;
    const positions = nodes.map((n) => {
      const top = nodeTopWorldPos(n);
      top.project(camera);
      const topPx = ndcToPixel(top.x, top.y, size);
      const center = nodeWorldPos(n);
      center.project(camera);
      const centerPx = ndcToPixel(center.x, center.y, size);
      return { id: n.id, px: topPx.px, py: topPx.py, cx: centerPx.px, cy: centerPx.py };
    });
    onPositions(positions);
  });

  return null;
}

// ---------------------------------------------------------------------------
// CameraSettleDetector: fires onSettle ~250ms after the camera stops moving.
// Compares camera matrix each frame; on change resets a debounce timer.
// ---------------------------------------------------------------------------

export function CameraSettleDetector({
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

export function computeOcclusionCounts(
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

export function RaycasterHelper({
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

export function NearestNTracker({
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

export function Scene({
  nodes,
  edges,
  selectedId,
  hoveredId,
  cameraRef,
  initialCamera3d,
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
  initialCamera3d?: Camera3D;
  onPickRequest: React.MutableRefObject<
    ((ndcX: number, ndcY: number, opts?: PickOptions) => string | null) | null
  >;
  onPositions: (positions: { id: string; px: number; py: number; cx: number; cy: number }[]) => void;
  onNearestN: (ids: Set<string>) => void;
  onCameraSettle: () => void;
}) {
  const nodeMap = new Map(nodes.map((n) => [n.id, n]));
  const hasRestoredCamera = initialCamera3d !== undefined;
  return (
    <>
      <CameraFitter nodes={nodes} hasRestoredCamera={hasRestoredCamera} />
      <CameraRefBridge cameraRef={cameraRef} initialCamera3d={initialCamera3d} />
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
