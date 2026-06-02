// scene-content.tsx — 3D scene render components for ThreeView.
// CameraFitter, GraphNode, PulseBead, SingleEdgeTube, GraphEdges,
// CameraRefBridge, LabelProjector, CameraSettleDetector,
// computeOcclusionCounts, RaycasterHelper, NearestNTracker, Scene.

import { useEffect, useRef, useMemo, createContext, useContext, useState } from "react";
import { useThree, useFrame } from "@react-three/fiber";
import * as THREE from "three";
import type { RFNode, RFEdge, NodeData, EdgeData } from "../types";
import type { Camera3D } from "../state/viewer/types";
import { useThreeStore } from "./store";
import { getPulseMap, claimDelivered, lastDeliveredAt } from "./pulse-state";
import { vscode } from "../vscode-api";
import { postLog } from "../log/post";
import { getPauseAdjustedNow } from "../state/run-status";
import {
  nodeRadius,
  boundingBox,
  nodeWorldPos,
  nodeTopWorldPos,
  ndcToPixel,
  portDir,
  portWorldPos,
  buildPortCurve,
} from "./geometry-helpers";
import type { PickOptions } from "./interaction-controls";

// ---------------------------------------------------------------------------
// Label LOD constants
// ---------------------------------------------------------------------------

/** Show label for the N nodes nearest to the camera, in addition to hovered/selected. */
export const NEAREST_N = 8;

// ---------------------------------------------------------------------------
// Procedural environment map — offline, no CDN, no preset fetch.
// Builds a PMREMGenerator env texture from a small gradient-sky scene and
// makes it available ONLY as the envMap on the node-body meshPhysicalMaterial.
// Does NOT assign scene.environment — edges, rings, and port spheres stay matte.
// ---------------------------------------------------------------------------

/** Context carrying the procedurally generated PMREM texture (or null before ready). */
const EnvTexContext = createContext<THREE.Texture | null>(null);

/**
 * Generates a PMREM env texture once and provides it via EnvTexContext.
 * No network requests — all geometry is inline. Does NOT touch scene.environment.
 */
function ProceduralEnvProvider({ children }: { children: React.ReactNode }) {
  const { gl } = useThree();
  const [envTex, setEnvTex] = useState<THREE.Texture | null>(null);

  useEffect(() => {
    const pmrem = new THREE.PMREMGenerator(gl);
    pmrem.compileEquirectangularShader();

    // Build a tiny scene: gradient sky sphere, no textures.
    const envScene = new THREE.Scene();

    // Sky hemisphere — top blue-white, horizon warm cream
    const skyGeo = new THREE.SphereGeometry(50, 16, 8);
    const skyMat = new THREE.MeshBasicMaterial({
      side: THREE.BackSide,
      vertexColors: true,
    });
    const skyMesh = new THREE.Mesh(skyGeo, skyMat);
    // Tint vertices top→bottom: cool blue at top, warm grey at equator
    const posAttr = skyGeo.attributes.position as THREE.BufferAttribute;
    const count = posAttr.count;
    const colors = new Float32Array(count * 3);
    for (let i = 0; i < count; i++) {
      const y = posAttr.getY(i);
      const t = Math.max(0, Math.min(1, (y / 50 + 1) / 2)); // 0 bottom → 1 top
      // top: #c0d8ff (soft blue), bottom: #ffe0c0 (warm cream)
      colors[i * 3 + 0] = 0.75 + (1.0 - 0.75) * (1 - t); // r
      colors[i * 3 + 1] = 0.85 + (0.88 - 0.85) * (1 - t); // g
      colors[i * 3 + 2] = 1.0 + (0.75 - 1.0) * (1 - t);  // b
    }
    skyGeo.setAttribute("color", new THREE.BufferAttribute(colors, 3));
    envScene.add(skyMesh);

    // Soft white fill light — bakes into env
    const fill = new THREE.AmbientLight(0xffffff, 1.2);
    envScene.add(fill);
    const key = new THREE.DirectionalLight(0xffeedd, 1.5);
    key.position.set(1, 2, 1);
    envScene.add(key);
    const rim = new THREE.DirectionalLight(0xaabbff, 0.6);
    rim.position.set(-2, 1, -1);
    envScene.add(rim);

    const tex = pmrem.fromScene(envScene, 0.04).texture;
    // Store in state — does NOT assign to scene.environment.
    setEnvTex(tex);

    return () => {
      tex.dispose();
      skyGeo.dispose();
      skyMat.dispose();
      pmrem.dispose();
    };
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <EnvTexContext.Provider value={envTex}>
      {children}
    </EnvTexContext.Provider>
  );
}

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
  selectedId,
  hoveredId,
}: {
  node: RFNode<NodeData>;
  selected: boolean;
  hovered: boolean;
  faded: boolean;
  selectedId?: string | null;
  hoveredId?: string | null;
}) {
  const envTex = useContext(EnvTexContext);
  const pos = nodeWorldPos(node);
  const r = nodeRadius(node);
  const fillHex = node.data?.fill ?? "#ffffff";
  const strokeHex = selected ? "#ffcc00"
    : hovered ? "#aaddff"
    : (node.data?.stroke ?? "#888888");

  // Memoize THREE.Color objects to avoid allocating on every render.
  const fillColor = useMemo(() => new THREE.Color(fillHex), [fillHex]);
  const strokeColor = useMemo(() => new THREE.Color(strokeHex), [strokeHex]);
  const emissiveFill = useMemo(() => new THREE.Color(0x000000), []);
  const emissiveStroke = useMemo(() => new THREE.Color(0x000000), []);

  const torusThick = (selected || hovered) ? r * 0.14 : r * 0.08;
  const fadeOpacity = 0.25;

  return (
    <group position={[pos.x, pos.y, pos.z]}>
      <mesh>
        <sphereGeometry args={[r, 16, 16]} />
        <meshPhysicalMaterial
          key={faded ? "faded" : "solid"}
          color={fillColor}
          transmission={1.0}
          thickness={r}
          roughness={0.12}
          ior={1.5}
          metalness={0}
          clearcoat={0.4}
          clearcoatRoughness={0.1}
          envMap={envTex ?? undefined}
          envMapIntensity={1.0}
          transparent
          opacity={faded ? fadeOpacity * 0.6 : 0.92}
          depthWrite={false}
        />
      </mesh>
      <mesh userData={{ nodeId: node.id, ring: true }}>
        <torusGeometry args={[r, torusThick, 8, 32]} />
        <meshStandardMaterial
          key={faded ? "faded" : "solid"}
          color={strokeColor}
          emissive={emissiveStroke}
          emissiveIntensity={0}
          transparent={faded}
          opacity={faded ? fadeOpacity : 1}
        />
      </mesh>
      <mesh raycast={() => null}>
        <sphereGeometry args={[r * 1.45, 16, 16]} />
        <meshBasicMaterial
          color="#ff5a00"
          transparent
          opacity={selected ? 0.5 : 0}
          side={THREE.DoubleSide}
          depthWrite={false}
        />
      </mesh>
      {/* Port spheres: one per input and output port, positioned on the node sphere surface */}
      {(node.data?.inputs ?? []).map((port) => {
        const dir = portDir(node, port.name, true);
        if (!dir) return null;
        const portId = `${node.id}:in:${port.name}`;
        const isSel = selectedId === portId;
        const isHov = hoveredId === portId;
        return (
          <mesh
            key={`in:${port.name}`}
            position={[dir.x * r, dir.y * r, dir.z * r]}
            scale={isSel ? 1.5 : isHov ? 1.3 : 1}
            userData={{ portId, nodeId: node.id, portName: port.name, isInput: true, port: true }}
          >
            <sphereGeometry args={[4, 8, 8]} />
            <meshStandardMaterial
              color={isSel ? "#ffcc00" : isHov ? "#aaddff" : strokeColor}
              emissive={isSel ? "#ffcc00" : isHov ? "#aaddff" : "#000000"}
              emissiveIntensity={isSel ? 0.7 : isHov ? 0.4 : 0}
            />
          </mesh>
        );
      })}
      {(node.data?.outputs ?? []).map((port) => {
        const dir = portDir(node, port.name, false);
        if (!dir) return null;
        const portId = `${node.id}:out:${port.name}`;
        const isSel = selectedId === portId;
        const isHov = hoveredId === portId;
        return (
          <mesh
            key={`out:${port.name}`}
            position={[dir.x * r, dir.y * r, dir.z * r]}
            scale={isSel ? 1.5 : isHov ? 1.3 : 1}
            userData={{ portId, nodeId: node.id, portName: port.name, isInput: false, port: true }}
          >
            <sphereGeometry args={[4, 8, 8]} />
            <meshStandardMaterial
              color={isSel ? "#ffcc00" : isHov ? "#aaddff" : strokeColor}
              emissive={isSel ? "#ffcc00" : isHov ? "#aaddff" : "#000000"}
              emissiveIntensity={isSel ? 0.7 : isHov ? 0.4 : 0}
            />
          </mesh>
        );
      })}
    </group>
  );
}

// ---------------------------------------------------------------------------
// Edges — 3D tube path using QuadraticBezierCurve3.
// Exit/entry points: point on each node's sphere surface facing the other node.
// ---------------------------------------------------------------------------

/**
 * Returns the center of `node` in world space.
 * `other` is accepted but ignored — kept for API symmetry with geometry-helpers.ts (kept in sync).
 */
function surfacePoint(node: RFNode<NodeData>, _other: RFNode<NodeData>): THREE.Vector3 {
  return nodeWorldPos(node);
}

// PulseBead: a bright sphere that travels along the port-to-port edge curve at the current pulse t.
// Duration is substrate-supplied (pulse.simLatencyMs); no speed constant needed.
// Driven by useFrame reading getPulseMap() imperatively (no React context needed).
// The curve is rebuilt each frame from src/tgt/handles via buildPortCurve so it tracks
// node moves in the same drag tick — identical curve to SingleEdgeTube.
export function PulseBead({
  edgeId,
  src,
  tgt,
  sourceHandle,
  targetHandle,
}: {
  edgeId: string;
  src: RFNode<NodeData>;
  tgt: RFNode<NodeData>;
  sourceHandle: string | null | undefined;
  targetHandle: string | null | undefined;
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
    const curve = buildPortCurve(src, tgt, sourceHandle, targetHandle);
    const duration = pulse.simLatencyMs;
    const t = Math.min((getPauseAdjustedNow() - pulse.startTime) / duration, 1);
    if (t >= 1) {
      mesh.visible = false;
      // Capture the prior claim before claiming, so we can report WHY a claim
      // failed: already-claimed-for-this-startTime (duplicate RAF tick at t>=1)
      // vs. claimed-for-a-different-startTime (stale pulse instance).
      const priorClaim = lastDeliveredAt(edgeId);
      const claimed = claimDelivered(edgeId, pulse.startTime);
      const claimReason = claimed
        ? "first-claim"
        : (priorClaim === pulse.startTime ? "already-claimed-this-startTime" : "claimed-other-startTime");
      // Use Go-authoritative slot identity from pulse data; fall back to React
      // prop only if the pulse carries empty strings (old binary compat).
      const resolvedTarget = (pulse.target !== "") ? pulse.target : tgt.id;
      const resolvedHandle = (pulse.targetHandle !== "") ? pulse.targetHandle : (targetHandle ?? "");
      postLog("pulse-deliver", { edgeId, target: pulse.target, targetHandle: pulse.targetHandle, propHandle: targetHandle ?? null, startTime: pulse.startTime, claimed, claimReason, priorClaim: priorClaim ?? null, resolvedHandleMissing: !resolvedHandle, resolvedTarget, resolvedHandle, posted: claimed && !!resolvedHandle });
      if (claimed) {
        if (resolvedHandle) {
          vscode.postMessage({ type: "delivered", target: resolvedTarget, targetHandle: resolvedHandle });
        }
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

export function SingleEdgeTube({ edgeId, src, tgt, faded, selected, sourceHandle, targetHandle }: { edgeId: string; src: RFNode<NodeData>; tgt: RFNode<NodeData>; faded: boolean; selected: boolean; sourceHandle?: string | null; targetHandle?: string | null }) {
  // Memoize geometry to avoid allocation every render — only rebuild when endpoints change.
  const p0key = `${src.id}:${tgt.id}:${src.position.x},${src.position.y},${tgt.position.x},${tgt.position.y}:${sourceHandle ?? ""}:${targetHandle ?? ""}`;
  const { tubeGeo, haloGeo } = useMemo(() => {
    // Port-to-port curve via shared helper — identical curve to what PulseBead samples.
    const portCurve = buildPortCurve(src, tgt, sourceHandle, targetHandle);

    // Sample the port-to-port bezier directly — endpoints are already on sphere surfaces,
    // so no t0/t1 surface-trim needed.
    const TUBE_RENDER_SAMPLES = 25;
    const visiblePts = portCurve.getPoints(TUBE_RENDER_SAMPLES);
    const tubeCurve = new THREE.CatmullRomCurve3(visiblePts);

    const _tubeGeo = new THREE.TubeGeometry(tubeCurve, 16, 1.5, 6, false);
    // Halo: concentric tube on the same curve, larger radius — reads as a glow around the core.
    const _haloGeo = new THREE.TubeGeometry(tubeCurve, 16, 5, 6, false);
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
      {!faded && <PulseBead edgeId={edgeId} src={src} tgt={tgt} sourceHandle={sourceHandle} targetHandle={targetHandle} />}
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
        return <SingleEdgeTube key={e.id} edgeId={e.id} src={s} tgt={t} faded={!!e.data?.faded} selected={e.id === selectedId} sourceHandle={e.sourceHandle ?? e.data?.sourceHandle ?? null} targetHandle={e.targetHandle ?? e.data?.targetHandle ?? null} />;
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
    // Restore saved camera state. If a quaternion is saved, apply it (preserves
    // any arcball rotation). If no quaternion is saved (fresh/default), fall back
    // to the square-on lock (looking straight down -z toward the z=0 plane).
    // NOTE: initialCamera3d is a dependency so that if load() populates
    // viewerState.camera3d after the first mount (which happens asynchronously
    // on reload), the effect re-runs and the saved position is applied.
    if (initialCamera3d) {
      cam.position.set(...initialCamera3d.position);
      if (initialCamera3d.quaternion) {
        const [qx, qy, qz, qw] = initialCamera3d.quaternion;
        cam.quaternion.set(qx, qy, qz, qw);
        cam.updateMatrixWorld(true);
        return;
      }
    }
    // No saved quaternion: default square-on orientation.
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

      if (opts?.ringOnly) {
        for (const hit of hits) {
          const hitObj = hit.object as THREE.Mesh;
          if (hitObj.userData?.ring === true) {
            return (hitObj.userData.nodeId as string) ?? null;
          }
        }
        return null;
      }

      if (opts?.portOnly) {
        for (const hit of hits) {
          const hitObj = hit.object as THREE.Mesh;
          if (hitObj.userData?.portId) {
            return hitObj.userData.portId as string;
          }
        }
        return null;
      }

      if (opts?.nodesOnly) {
        // Iterate all hits to find the first node that isn't excluded.
        // A port sphere hit resolves to its node (via userData.nodeId).
        for (const hit of hits) {
          const hitObj = hit.object as THREE.Mesh;
          if (hitObj.userData?.edgeId) continue; // skip edges
          if (hitObj.userData?.port) {
            // Port sphere — resolve to its owning node.
            const nId = hitObj.userData.nodeId as string;
            if (opts.excludeId && nId === opts.excludeId) continue;
            return nId;
          }
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

      // Default path: scan ALL hits nearest-first, capture nearest of each category.
      const PORT_HIT_TOL = 8;
      let portHit: { id: string; dist: number } | null = null;
      let edgeHit: { id: string; dist: number } | null = null;
      let nodeHit: { id: string; dist: number } | null = null;

      for (const hit of hits) {
        const hitObj = hit.object as THREE.Mesh;
        if (!portHit && hitObj.userData?.portId) {
          portHit = { id: hitObj.userData.portId as string, dist: hit.distance };
          continue;
        }
        if (!edgeHit && hitObj.userData?.edgeId) {
          edgeHit = { id: hitObj.userData.edgeId as string, dist: hit.distance };
          continue;
        }
        if (!nodeHit) {
          const hitPoint = hitObj.parent;
          if (!hitPoint) continue;
          for (const n of nodes) {
            const wp = nodeWorldPos(n);
            if (
              Math.abs(hitPoint.position.x - wp.x) < 1 &&
              Math.abs(hitPoint.position.y - wp.y) < 1
            ) {
              nodeHit = { id: n.id, dist: hit.distance };
              break;
            }
          }
        }
      }

      // Precedence: port wins over node if within tolerance (covers embedded half of port sphere).
      if (portHit && (!nodeHit || portHit.dist <= nodeHit.dist + PORT_HIT_TOL)) {
        return portHit.id;
      }
      if (edgeHit && nodeHit) {
        return edgeHit.dist <= nodeHit.dist ? edgeHit.id : nodeHit.id;
      }
      if (edgeHit) return edgeHit.id;
      if (nodeHit) return nodeHit.id;
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
    <ProceduralEnvProvider>
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
          selectedId={selectedId}
          hoveredId={hoveredId}
        />
      ))}
      <GraphEdges edges={edges} nodeMap={nodeMap} selectedId={selectedId} />
    </ProceduralEnvProvider>
  );
}
