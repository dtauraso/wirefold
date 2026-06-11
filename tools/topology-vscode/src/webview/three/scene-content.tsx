// scene-content.tsx — 3D scene render components for ThreeView.
// CameraFitter, GraphNode, PulseBead, SingleEdgeTube, GraphEdges,
// CameraRefBridge, LabelProjector, CameraSettleDetector,
// computeOcclusionCounts, RaycasterHelper, NearestNTracker, Scene.

import React, { useEffect, useRef, useMemo, createContext, useContext, useState } from "react";
import { useThree, useFrame } from "@react-three/fiber";
import * as THREE from "three";
import type { RFNode, RFEdge, NodeData, EdgeData } from "../types";
import type { Camera3D } from "../state/viewer/types";
import { useThreeStore } from "./store";
import {
  nodeRadius,
  boundingBox,
  nodeWorldPos,
  nodeTopWorldPos,
  ndcToPixel,
  portDir,
} from "./geometry-helpers";
import { useEdgeGeometryStore } from "./edge-geometry";
import { useChainBeadStore } from "./chain-bead-geometry";
import { usePulseLitStore } from "./pulse-lit";
import type { PickOptions } from "./interaction-controls";
// Shading PARAMETER values are Go-authoritative (MODEL.md / Phase 4). They are
// generated from nodes/Wiring/shading_params.go into ../../schema/shading-params.
// This module sources NO shading values of its own — it only runs the GPU:
// creates THREE materials, bakes the PMREM env map, and binds these Go params.
import {
  SHADING_PARAM_NODE_TRANSMISSION,
  SHADING_PARAM_NODE_THICKNESS,
  SHADING_PARAM_NODE_ROUGHNESS,
  SHADING_PARAM_NODE_IOR,
  SHADING_PARAM_NODE_METALNESS,
  SHADING_PARAM_NODE_CLEARCOAT,
  SHADING_PARAM_NODE_CLEARCOAT_ROUGHNESS,
  SHADING_PARAM_NODE_ENV_MAP_INTENSITY,
  SHADING_PARAM_NODE_OPACITY,
  SHADING_PARAM_NODE_FADE_OPACITY,
  SHADING_PARAM_NODE_FADE_BODY_MUL,
  SHADING_PARAM_ENV_SKY_TOP_R,
  SHADING_PARAM_ENV_SKY_TOP_G,
  SHADING_PARAM_ENV_SKY_TOP_B,
  SHADING_PARAM_ENV_SKY_BOTTOM_R,
  SHADING_PARAM_ENV_SKY_BOTTOM_G,
  SHADING_PARAM_ENV_SKY_BOTTOM_B,
  SHADING_PARAM_ENV_SKY_RADIUS,
  SHADING_PARAM_ENV_AMBIENT_COLOR,
  SHADING_PARAM_ENV_AMBIENT_INTENSITY,
  SHADING_PARAM_ENV_KEY_COLOR,
  SHADING_PARAM_ENV_KEY_INTENSITY,
  SHADING_PARAM_ENV_RIM_COLOR,
  SHADING_PARAM_ENV_RIM_INTENSITY,
  SHADING_PARAM_ENV_PMREM_BLUR,
  SHADING_PARAM_SCENE_AMBIENT_INTENSITY,
  SHADING_PARAM_SCENE_DIR_INTENSITY,
  SHADING_PARAM_TUBE_COLOR,
  SHADING_PARAM_BEAD_COLOR,
  SHADING_PARAM_BEAD_EMISSIVE,
} from "../../schema/shading-params";

// ---------------------------------------------------------------------------
// Label LOD constants
// ---------------------------------------------------------------------------

/** Show label for the N nodes nearest to the camera, in addition to hovered/selected. */
export const NEAREST_N = 8;

type InitPulseBeadProps = { pr: number; faded: boolean; fadeOpacityInner: number };

// Shared bead renderer: a sphere of `fill`, with an optional ring of `ring`.
function StyledPulseBead({ pr, faded, fadeOpacityInner, fill, ring }: InitPulseBeadProps & { fill: string; ring?: string }) {
  return (
    <>
      <mesh raycast={() => null}>
        <sphereGeometry args={[pr, 16, 16]} />
        <meshStandardMaterial color={fill} emissiveIntensity={0} transparent={faded} opacity={faded ? fadeOpacityInner : 1} />
      </mesh>
      {ring !== undefined && (
        <mesh raycast={() => null}>
          <torusGeometry args={[pr, pr * 0.12, 8, 24]} />
          <meshStandardMaterial color={ring} emissiveIntensity={0} transparent={faded} opacity={faded ? fadeOpacityInner : 1} />
        </mesh>
      )}
    </>
  );
}

function WhiteRingPulseBead(p: InitPulseBeadProps) { return <StyledPulseBead {...p} fill="#ffffff" ring="#000000" />; }
function BlackRingPulseBead(p: InitPulseBeadProps) { return <StyledPulseBead {...p} fill="#000000" ring="#000000" />; }
function DefaultPulseBead(p: InitPulseBeadProps) { return <StyledPulseBead {...p} fill="#888888" ring="#000000" />; }

// Map each raw init value (Go emits 0/1) to the component that renders its bead.
const INIT_PULSE_COMPONENTS: Record<number, React.FC<InitPulseBeadProps>> = {
  0: WhiteRingPulseBead,
  1: BlackRingPulseBead,
};

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

    // Sky hemisphere — top neutral, horizon warm cream (Go-supplied tint params).
    const skyGeo = new THREE.SphereGeometry(SHADING_PARAM_ENV_SKY_RADIUS, 16, 8);
    const skyMat = new THREE.MeshBasicMaterial({
      side: THREE.BackSide,
      vertexColors: true,
    });
    const skyMesh = new THREE.Mesh(skyGeo, skyMat);
    // Tint vertices top→bottom by lerping Go's top/bottom sky colors. t=1 at the
    // top, t=0 at the horizon; channel = top + (bottom - top) * (1 - t).
    const posAttr = skyGeo.attributes.position as THREE.BufferAttribute;
    const count = posAttr.count;
    const colors = new Float32Array(count * 3);
    for (let i = 0; i < count; i++) {
      const y = posAttr.getY(i);
      const t = Math.max(0, Math.min(1, (y / SHADING_PARAM_ENV_SKY_RADIUS + 1) / 2)); // 0 bottom → 1 top
      colors[i * 3 + 0] = SHADING_PARAM_ENV_SKY_TOP_R + (SHADING_PARAM_ENV_SKY_BOTTOM_R - SHADING_PARAM_ENV_SKY_TOP_R) * (1 - t); // r
      colors[i * 3 + 1] = SHADING_PARAM_ENV_SKY_TOP_G + (SHADING_PARAM_ENV_SKY_BOTTOM_G - SHADING_PARAM_ENV_SKY_TOP_G) * (1 - t); // g
      colors[i * 3 + 2] = SHADING_PARAM_ENV_SKY_TOP_B + (SHADING_PARAM_ENV_SKY_BOTTOM_B - SHADING_PARAM_ENV_SKY_TOP_B) * (1 - t); // b
    }
    skyGeo.setAttribute("color", new THREE.BufferAttribute(colors, 3));
    envScene.add(skyMesh);

    // Soft white fill light — bakes into env (Go-supplied color + intensity).
    const fill = new THREE.AmbientLight(new THREE.Color(SHADING_PARAM_ENV_AMBIENT_COLOR), SHADING_PARAM_ENV_AMBIENT_INTENSITY);
    envScene.add(fill);
    const key = new THREE.DirectionalLight(new THREE.Color(SHADING_PARAM_ENV_KEY_COLOR), SHADING_PARAM_ENV_KEY_INTENSITY);
    key.position.set(1, 2, 1);
    envScene.add(key);
    const rim = new THREE.DirectionalLight(new THREE.Color(SHADING_PARAM_ENV_RIM_COLOR), SHADING_PARAM_ENV_RIM_INTENSITY);
    rim.position.set(-2, 1, -1);
    envScene.add(rim);

    const tex = pmrem.fromScene(envScene, SHADING_PARAM_ENV_PMREM_BLUR).texture;
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
  const fadeOpacity = SHADING_PARAM_NODE_FADE_OPACITY;

  return (
    <group position={[pos.x, pos.y, pos.z]}>
      <mesh>
        <sphereGeometry args={[r, 16, 16]} />
        <meshPhysicalMaterial
          key={faded ? "faded" : "solid"}
          color={fillColor}
          transmission={SHADING_PARAM_NODE_TRANSMISSION}
          thickness={SHADING_PARAM_NODE_THICKNESS}
          roughness={SHADING_PARAM_NODE_ROUGHNESS}
          ior={SHADING_PARAM_NODE_IOR}
          metalness={SHADING_PARAM_NODE_METALNESS}
          clearcoat={SHADING_PARAM_NODE_CLEARCOAT}
          clearcoatRoughness={SHADING_PARAM_NODE_CLEARCOAT_ROUGHNESS}
          envMap={envTex ?? undefined}
          envMapIntensity={SHADING_PARAM_NODE_ENV_MAP_INTENSITY}
          transparent
          opacity={faded ? fadeOpacity * SHADING_PARAM_NODE_FADE_BODY_MUL : SHADING_PARAM_NODE_OPACITY}
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
      {/* Init pulses: render data.init values as small spheres inside the node body */}
      {(() => {
        // data.init lives in node.data.nodeData (the verbatim spec data blob)
        const rawNodeData = node.data?.nodeData as Record<string, unknown> | undefined;
        const init = rawNodeData?.["init"];
        if (!Array.isArray(init) || init.length === 0) return null;
        const n = init.length;
        // Match pulse-bead radius (4), but scale down if the row doesn't fit within the node diameter.
        // Layout: n spheres of radius pr with gap = pr * 0.3 between adjacent spheres.
        // Total width = n * 2*pr + (n-1)*0.3*pr = pr * (2n + 0.3*(n-1))
        // Constraint: total width ≤ 2*r  →  pr ≤ 2*r / (2n + 0.3*(n-1))
        const PULSE_BEAD_R = 5;
        const GAP_RATIO = 0.3; // gap = pr * GAP_RATIO
        const fitFactor = 2 * r / (2 * n + GAP_RATIO * (n - 1));
        const pr = Math.min(PULSE_BEAD_R, fitFactor);
        const gap = pr * GAP_RATIO;
        const totalWidth = n * 2 * pr + (n - 1) * gap;
        const startX = -totalWidth / 2 + pr; // center of leftmost sphere
        const zFront = 0; // beads sit on the torus center plane
        const fadeOpacityInner = 0.25;
        return (init as number[]).map((val: number, idx: number) => {
          const x = startX + idx * (2 * pr + gap);
          // Map the raw init value to its bead component; Go keeps emitting raw 0/1.
          const Bead = INIT_PULSE_COMPONENTS[val] ?? DefaultPulseBead;
          return (
            <group key={idx} position={[x, 0, zFront]}>
              <Bead pr={pr} faded={faded} fadeOpacityInner={fadeOpacityInner} />
            </group>
          );
        });
      })()}
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
// Edges — 3D tube path using LineCurve3 (straight segment).
// Exit/entry points: point on each node's sphere surface facing the other node.
// ---------------------------------------------------------------------------

/**
 * Returns the center of `node` in world space.
 * `other` is accepted but ignored — kept for API symmetry with geometry-helpers.ts (kept in sync).
 */
function surfacePoint(node: RFNode<NodeData>, _other: RFNode<NodeData>): THREE.Vector3 {
  return nodeWorldPos(node);
}

// The single moving PulseBead (one sphere driven by the "position" event /
// getPulseMap) was removed: the pulse now reads purely as chain beads recoloring
// to the pulse color as it passes (EdgeBeadChain). The underlying pulse-state
// store / PacedWire are left intact — we simply no longer render that sphere, so
// there is no double pulse.

// EdgeBeadChain: the wire, drawn as a chain of beads (MODEL.md "The wire is a
// chain of bead-items"). Go runs a chain of bead-item goroutines per edge and
// STREAMS each item's world position (chain-bead trace) and pulse-highlight
// (pulse-lit trace); pump.ts writes them to the chain-bead-geometry / pulse-lit
// stores. This component PLOTS ONLY: one sphere per chain bead at its Go-streamed
// position, default-colored the edge (tube) color, recolored to the pulse (bead)
// color while its pulse-lit entry has lit=true. TS computes no positions, no
// timing — both come from the stores. One InstancedMesh per edge.
const _EDGE_BEAD_COLOR = new THREE.Color(SHADING_PARAM_TUBE_COLOR);
const _PULSE_BEAD_COLOR = new THREE.Color(SHADING_PARAM_BEAD_COLOR);

export function EdgeBeadChain({ edgeId, faded }: { edgeId: string; faded: boolean }) {
  const beads = useChainBeadStore((s) => s.beads[edgeId]);
  const lit = usePulseLitStore((s) => s.lit[edgeId]);
  const meshRef = useRef<THREE.InstancedMesh>(null);

  // Stable, sorted bead ids so instance index ↔ bead id is deterministic.
  const beadIds = useMemo(
    () => (beads ? Object.keys(beads).map(Number).sort((a, b) => a - b) : []),
    [beads],
  );

  // Re-instance only when the SET of beads changes (births/retirements). Position
  // and color updates run every frame against the current store snapshot.
  const count = beadIds.length;

  useFrame(() => {
    const mesh = meshRef.current;
    if (!mesh || count === 0) return;
    const curBeads = useChainBeadStore.getState().beads[edgeId];
    const curLit = usePulseLitStore.getState().lit[edgeId];
    if (!curBeads) return;
    const dummy = _beadDummy;
    for (let i = 0; i < count; i++) {
      const id = beadIds[i];
      const p = curBeads[id];
      if (!p) continue;
      dummy.position.set(p.x, p.y, p.z);
      dummy.updateMatrix();
      mesh.setMatrixAt(i, dummy.matrix);
      const isLit = !!curLit?.[id]?.lit;
      mesh.setColorAt(i, isLit ? _PULSE_BEAD_COLOR : _EDGE_BEAD_COLOR);
    }
    mesh.instanceMatrix.needsUpdate = true;
    if (mesh.instanceColor) mesh.instanceColor.needsUpdate = true;
  });

  // Startup race: Go has not streamed this edge's chain yet → draw nothing.
  if (count === 0 || faded) return null;

  return (
    <instancedMesh
      key={count}
      ref={meshRef}
      args={[undefined, undefined, count]}
      userData={{ edgeId }}
    >
      <sphereGeometry args={[4, 8, 8]} />
      <meshStandardMaterial
        emissive={new THREE.Color(SHADING_PARAM_BEAD_EMISSIVE)}
        emissiveIntensity={0}
      />
    </instancedMesh>
  );
}

// Shared scratch object for per-frame instance matrix composition (no per-frame
// allocation). Geometry math here is identity transform only — positions come
// straight from Go's stored values.
const _beadDummy = new THREE.Object3D();

export function SingleEdgeTube({ edgeId, faded, selected }: { edgeId: string; faded: boolean; selected: boolean }) {
  // Go is the authoritative holder of this edge's segment (Phase 3, MODEL.md). It
  // streams the endpoints (geometry trace) on load and on every node-move;
  // pump.ts writes them to the edge-geometry store. We subscribe to THIS edge's
  // endpoints and draw the tube from them — TS computes no geometry.
  const seg = useEdgeGeometryStore((s) => s.segments[edgeId]);

  // Stable key over Go's streamed endpoints — rebuild the tube only when they
  // change (e.g. a drag re-streams them).
  const segKey = seg
    ? `${seg.start.x},${seg.start.y},${seg.start.z}:${seg.end.x},${seg.end.y},${seg.end.z}`
    : "";
  const { haloGeo } = useMemo(() => {
    if (!seg) return { haloGeo: null as THREE.TubeGeometry | null };
    const start = new THREE.Vector3(seg.start.x, seg.start.y, seg.start.z);
    const end = new THREE.Vector3(seg.end.x, seg.end.y, seg.end.z);
    // Wire is a straight line: P(t) = Start + t*(End-Start).
    const tubeCurve = new THREE.LineCurve3(start, end);
    // Halo: concentric tube on the segment — the wide pick target / selection glow.
    const _haloGeo = new THREE.TubeGeometry(tubeCurve, 1, 5, 6, false);
    return { haloGeo: _haloGeo };
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [segKey]);

  // Until Go streams this edge's segment, draw nothing (geometry arrives on load).
  if (!haloGeo) return null;

  // The visible wire is now the bead chain (EdgeBeadChain). The base tube and the
  // single moving PulseBead are no longer rendered — the pulse reads purely as
  // chain beads recoloring as it passes. This component now contributes ONLY the
  // selection halo, which doubles as the wide pick target along the segment.
  return (
    <mesh geometry={haloGeo} userData={{ edgeId }}>
      <meshBasicMaterial
        color="#ff5a00"
        transparent
        opacity={selected ? 0.6 : 0}
        side={THREE.DoubleSide}
        depthWrite={false}
      />
    </mesh>
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
        // Endpoints still gate rendering (a dangling edge with a missing node draws
        // nothing), but the tube SHAPE comes from Go's streamed curve, not from these.
        const s = nodeMap.get(e.source);
        const t = nodeMap.get(e.target);
        if (!s || !t) return null;
        const faded = !!e.data?.faded;
        return (
          <React.Fragment key={e.id}>
            {/* Visible wire: chain of beads at Go's streamed positions. */}
            <EdgeBeadChain edgeId={e.id} faded={faded} />
            {/* Selection halo / wide pick target (no visible tube anymore). */}
            <SingleEdgeTube edgeId={e.id} faded={faded} selected={e.id === selectedId} />
          </React.Fragment>
        );
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
      <ambientLight intensity={SHADING_PARAM_SCENE_AMBIENT_INTENSITY} />
      <directionalLight position={[0, 0, 10]} intensity={SHADING_PARAM_SCENE_DIR_INTENSITY} />
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
