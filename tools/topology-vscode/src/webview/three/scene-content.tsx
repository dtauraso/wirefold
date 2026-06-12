// scene-content.tsx — 3D scene render components for ThreeView.
// CameraFitter, GraphNode, PulseBead, InteriorBeads, SingleEdgeTube, GraphEdges,
// CameraRefBridge, LabelProjector, CameraSettleDetector,
// computeOcclusionCounts, RaycasterHelper, NearestNTracker, Scene.

import React, { useEffect, useRef, useMemo, createContext, useContext, useState } from "react";
import { useThree, useFrame } from "@react-three/fiber";
import * as THREE from "three";
import type { RFNode, RFEdge, NodeData, EdgeData } from "../types";
import type { Camera3D } from "../state/viewer/types";
import { useThreeStore } from "./store";
import { useNodeGeometryStore, getNodeGeometry } from "./node-geometry";
import {
  nodeRadius,
  boundingBox,
  nodeWorldPos,
  nodeTopWorldPos,
  ndcToPixel,
  portDir,
} from "./geometry-helpers";
import { postLog } from "../log/post";
import { getPulseMap } from "./pulse-state";
import { getInteriorBeadMap, interiorBeadKey } from "./interior-bead-state";
import { useEdgeGeometryStore } from "./edge-geometry";
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
  SHADING_PARAM_TUBE_EMISSIVE,
  SHADING_PARAM_TUBE_EMISSIVE_INTENSITY,
} from "../../schema/shading-params";

// ---------------------------------------------------------------------------
// Label LOD constants
// ---------------------------------------------------------------------------

/** Show label for the N nodes nearest to the camera, in addition to hovered/selected. */
export const NEAREST_N = 8;

// Single source of truth for value→appearance. Both the interior buffer beads
// (inside node 1) and the animated edge bead derive their fill/ring colors here, so
// they cannot visually diverge. (The former static data.init bead components were
// removed when node 1's interior switched to the live node-bead stream.)
const VALUE_BEAD_STYLE: Record<number, { fill: string; ring: string }> = {
  0: { fill: "#ffffff", ring: "#000000" },
  1: { fill: "#000000", ring: "#000000" },
};
// Only 0 and 1 are valid bead values. A value outside the map (including a
// missing/undefined value) returns undefined — the caller hides the bead rather
// than drawing a grey/fake fallback. With Go no longer placing -1 on a wire, a
// non-0/1 bead is a bug, not a colour to paint.
function beadStyleForValue(v: number | null | undefined): { fill: string; ring: string } | undefined {
  return v == null ? undefined : VALUE_BEAD_STYLE[v];
}

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
  // Subscribe to the Go node-geometry store so the effect re-runs when Go's
  // node-geometry trace stream populates centers after load (it lands ~1 frame
  // AFTER store:load). The first fit must wait for this, or it frames the
  // pre-emit fallback coords instead of the real Go-streamed centers.
  const geoms = useNodeGeometryStore((s) => s.geoms);
  // Track the last epoch we actually fitted for. The effect may run several
  // times as size/nodes/geometry settle on first load; we fit only the first
  // time everything is ready for a given epoch, and never again until the next load.
  const lastFittedEpoch = useRef<number>(-1);
  useEffect(() => {
    // Already fitted for this load epoch (e.g. a node drag re-triggered us).
    if (lastFittedEpoch.current === loadEpoch) return;
    // Skip if no content or canvas not yet sized — re-runs when either settles.
    if (nodes.length === 0) return;
    if (size.width === 0 || size.height === 0) return;
    // Skip auto-fit when the saved camera is being restored.
    if (hasRestoredCamera) return;
    // Wait until Go geometry has landed for EVERY current node, so we frame the
    // ACTUAL rendered centers (g.center) and not the pre-emit fallback. Re-runs
    // when `geoms` gains the last node's center. (geoms read so the dep is live.)
    void geoms;
    if (!nodes.every((n) => getNodeGeometry(n.id) !== undefined)) return;
    lastFittedEpoch.current = loadEpoch;
    const persp = camera as THREE.PerspectiveCamera;
    const PAD = 80;
    // boundingBox reads nodeWorldPos (Go g.center, already Three y-up world
    // coords) — the SAME source GraphNode renders the node group from. So the
    // center is used DIRECTLY with no y-negation: negating here (the old bug)
    // framed the mirror-image y and pushed the nodes off-screen until manual Fit.
    // This now matches HomeButton's math (camera-ui.tsx), which works.
    const { minX, maxX, minY, maxY } = boundingBox(nodes);
    const gw = (maxX - minX) + 2 * PAD;
    const gh = (maxY - minY) + 2 * PAD;
    const cx = (minX + maxX) / 2;
    const cy = (minY + maxY) / 2; // nodeWorldPos is already y-up world — no negate
    postLog("lifecycle", { phase: "camera-fit", nodeCount: nodes.length, cx, cy, minX, maxX, minY, maxY });
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
  // Re-run when size/nodes/geometry settle; the lastFittedEpoch ref makes it fit
  // once per load epoch and never on drag (same epoch).
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loadEpoch, size.width, size.height, nodes.length, geoms]);
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
      {/* Interior beads: node 1's 2x2 buffer from the live node-bead stream, rendered
          as CHILDREN of this node group at Go-given NODE-LOCAL offsets. Because they
          inherit the group's transform, they ride the node on drag (world position =
          node center + offset, composed by the scene graph). Every GraphNode mounts
          the 4 slots; non-Input nodes have no store entries → each slot hides itself. */}
      <InteriorBeads nodeId={node.id} />

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

// PulseBead: a bright sphere drawn ON the wire at its Go-owned fractional progress.
// Go owns the clock and the bead's fraction along the wire; this component PLOTS ONLY —
// each frame it reads pulse.frac (the bead's progress t, 0..1) from getPulseMap() and
// places the bead at lerp(start, end, frac) on the SAME segment SingleEdgeTube draws,
// read from useEdgeGeometryStore (Go's wireSegment Start/End, re-emitted on every
// node-move). Because bead and tube share one segment source, the bead is provably on
// the line every frame and rides the wire as the node moves — no lag, no drift.
// No curve sampling beyond the linear lerp, no clock, no delivery message: the renderer
// is told where the bead is (segment + fraction), never asked when it arrived (MODEL.md).
// pulse.frac is now LIVE — it is the progress used to place the bead on the live wire.
// Go's emitted x,y,z on the position event (pulse.pos) is now unused by the bead; it is
// left on the bridge for now (no Go/bridge churn).
export function PulseBead({
  edgeId,
}: {
  edgeId: string;
}) {
  const groupRef = useRef<THREE.Group>(null);
  const sphereMatRef = useRef<THREE.MeshStandardMaterial>(null);
  const torusMatRef = useRef<THREE.MeshStandardMaterial>(null);
  // SAME source SingleEdgeTube subscribes to — bead and tube cannot diverge.
  const seg = useEdgeGeometryStore((s) => s.segments[edgeId]);

  useFrame(() => {
    const pulse = getPulseMap().get(edgeId);
    const group = groupRef.current;
    if (!group) return;
    // Hidden until there is a pulse with a fraction AND Go has streamed this edge's
    // segment (startup race: no segment yet → render nothing, no crash).
    if (!pulse || pulse.frac == null || !seg) {
      group.visible = false;
      return;
    }
    // Render placement of Go-owned values: place the bead at its Go-supplied fraction
    // along the Go-supplied segment. Same exception class as nodeWorldPos.
    const f = pulse.frac;
    group.position.set(
      seg.start.x + f * (seg.end.x - seg.start.x),
      seg.start.y + f * (seg.end.y - seg.start.y),
      seg.start.z + f * (seg.end.z - seg.start.z),
    );
    // Color the bead by the value it carries — same map as the static init beads.
    // A non-0/1 value has no style → hide rather than paint a fallback.
    const style = beadStyleForValue(pulse.value);
    if (!style) {
      group.visible = false;
      return;
    }
    sphereMatRef.current?.color.set(style.fill);
    torusMatRef.current?.color.set(style.ring);
    group.visible = true;
  });

  return (
    <group ref={groupRef} visible={false}>
      <mesh raycast={() => null}>
        <sphereGeometry args={[4, 16, 16]} />
        <meshStandardMaterial ref={sphereMatRef} emissiveIntensity={0} />
      </mesh>
      <mesh raycast={() => null}>
        <torusGeometry args={[4, 4 * 0.12, 8, 24]} />
        <meshStandardMaterial ref={torusMatRef} emissiveIntensity={0} />
      </mesh>
    </group>
  );
}

// InteriorBeads: renders node 1's 2x2 interior buffer from the live node-bead
// stream as CHILDREN of the node group (mounted inside GraphNode). Go owns the slot
// offsets (NODE-LOCAL, relative to the node center) and the present/absent state;
// this component PLOTS ONLY — it reads getInteriorBeadMap() imperatively each frame
// and places each PRESENT slot's mesh at its Go-supplied local offset, hiding empty
// (popped) slots. Because the mesh is a child of the node group, its world position
// = node center + offset is composed by the scene graph, so the beads ride the node
// on drag with no re-emit. TS computes no geometry (no interior layout math).
// Discrete positions this phase (beads snap; no slide yet).
const INTERIOR_SLOTS: { row: number; col: number }[] = [
  { row: 0, col: 0 }, { row: 0, col: 1 },
  { row: 1, col: 0 }, { row: 1, col: 1 },
];
const INTERIOR_BEAD_R = 5;

function InteriorSlotBead({ nodeId, row, col }: { nodeId: string; row: number; col: number }) {
  const groupRef = useRef<THREE.Group>(null);
  const sphereMatRef = useRef<THREE.MeshStandardMaterial>(null);
  const torusMatRef = useRef<THREE.MeshStandardMaterial>(null);

  useFrame(() => {
    const group = groupRef.current;
    if (!group) return;
    const slot = getInteriorBeadMap().get(interiorBeadKey(nodeId, row, col));
    // Hidden until Go has streamed this slot AND it is present (popped slots carry
    // present=false → hide). No geometry: place the mesh at Go's NODE-LOCAL offset.
    // The parent node group supplies the center, so this is the offset verbatim.
    if (!slot || !slot.present) {
      group.visible = false;
      return;
    }
    group.position.set(slot.pos.x, slot.pos.y, slot.pos.z);
    // A non-0/1 value has no style → hide rather than paint a fallback.
    const style = beadStyleForValue(slot.value);
    if (!style) {
      group.visible = false;
      return;
    }
    sphereMatRef.current?.color.set(style.fill);
    torusMatRef.current?.color.set(style.ring);
    group.visible = true;
  });

  return (
    <group ref={groupRef} visible={false}>
      <mesh raycast={() => null}>
        <sphereGeometry args={[INTERIOR_BEAD_R, 16, 16]} />
        <meshStandardMaterial ref={sphereMatRef} emissiveIntensity={0} />
      </mesh>
      <mesh raycast={() => null}>
        <torusGeometry args={[INTERIOR_BEAD_R, INTERIOR_BEAD_R * 0.12, 8, 24]} />
        <meshStandardMaterial ref={torusMatRef} emissiveIntensity={0} />
      </mesh>
    </group>
  );
}

export function InteriorBeads({ nodeId }: { nodeId: string }) {
  return (
    <>
      {INTERIOR_SLOTS.map((s) => (
        <InteriorSlotBead key={`${s.row}:${s.col}`} nodeId={nodeId} row={s.row} col={s.col} />
      ))}
    </>
  );
}

export function SingleEdgeTube({ edgeId, faded, selected }: { edgeId: string; faded: boolean; selected: boolean }) {
  // Go is the authoritative holder of this edge's segment (Phase 3, MODEL.md). It
  // streams the endpoints (geometry trace) on load and on every node-move;
  // pump.ts writes them to the edge-geometry store. We subscribe to THIS edge's
  // endpoints and draw the tube from them — TS computes no geometry. A dragged node
  // re-streams its touched edges' segments, so the wire follows ~1 frame behind.
  const seg = useEdgeGeometryStore((s) => s.segments[edgeId]);

  // Stable key over Go's streamed endpoints — rebuild the tube only when they
  // change (e.g. a drag re-streams them).
  const segKey = seg
    ? `${seg.start.x},${seg.start.y},${seg.start.z}:${seg.end.x},${seg.end.y},${seg.end.z}`
    : "";
  const { tubeGeo, haloGeo } = useMemo(() => {
    if (!seg) return { tubeGeo: null as THREE.TubeGeometry | null, haloGeo: null as THREE.TubeGeometry | null };
    const start = new THREE.Vector3(seg.start.x, seg.start.y, seg.start.z);
    const end = new THREE.Vector3(seg.end.x, seg.end.y, seg.end.z);
    // Wire is a straight line: P(t) = Start + t*(End-Start).
    const tubeCurve = new THREE.LineCurve3(start, end);
    const _tubeGeo = new THREE.TubeGeometry(tubeCurve, 1, 1.5, 6, false);
    // Halo: concentric tube on the same segment, larger radius — reads as a glow around the core.
    const _haloGeo = new THREE.TubeGeometry(tubeCurve, 1, 5, 6, false);
    return { tubeGeo: _tubeGeo, haloGeo: _haloGeo };
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [segKey]);

  // Until Go streams this edge's segment, draw nothing (geometry arrives on load).
  if (!tubeGeo || !haloGeo) {
    return <>{!faded && <PulseBead edgeId={edgeId} />}</>;
  }

  return (
    <>
      {/* Always-lit base tube — emissive so it reads at any camera angle */}
      <mesh geometry={tubeGeo} userData={{ edgeId }}>
        <meshStandardMaterial
          key={faded ? "faded" : "solid"}
          color={SHADING_PARAM_TUBE_COLOR}
          emissive={new THREE.Color(SHADING_PARAM_TUBE_EMISSIVE)}
          emissiveIntensity={SHADING_PARAM_TUBE_EMISSIVE_INTENSITY}
          transparent={faded}
          opacity={faded ? SHADING_PARAM_NODE_FADE_OPACITY : 1}
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
      {/* Pulse bead: plotted at Go's streamed position (Phase 2). */}
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
        // Node-presence gate (a dangling edge with a missing node draws nothing).
        // The wire segment itself is sourced from Go's edge-geometry store inside
        // SingleEdgeTube — Go re-streams it on node-move, so the wire tracks drags.
        const s = nodeMap.get(e.source);
        const t = nodeMap.get(e.target);
        if (!s || !t) return null;
        return <SingleEdgeTube key={e.id} edgeId={e.id} faded={!!e.data?.faded} selected={e.id === selectedId} />;
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

      // Node-favoring margin: the wide invisible edge selection-halo runs node→node,
      // so over a node the halo surface is at/closer than the node sphere. Without a
      // bias the edge would win and node-drag would silently break. An edge only wins
      // when it is clearly closer than the node by this margin (world units).
      const EDGE_BIAS = 2;

      // Precedence: port wins over node if within tolerance (covers embedded half of port sphere).
      let picked: string | null = null;
      if (portHit && (!nodeHit || portHit.dist <= nodeHit.dist + PORT_HIT_TOL)) {
        picked = portHit.id;
      } else if (edgeHit && nodeHit) {
        picked = edgeHit.dist < nodeHit.dist - EDGE_BIAS ? edgeHit.id : nodeHit.id;
      } else if (edgeHit) {
        picked = edgeHit.id;
      } else if (nodeHit) {
        picked = nodeHit.id;
      }

      return picked;
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
      {/* Interior beads are now mounted INSIDE each GraphNode group (at Go-given
          node-local offsets) so they ride the node on move — no top-level mount. */}
      <GraphEdges edges={edges} nodeMap={nodeMap} selectedId={selectedId} />
    </ProceduralEnvProvider>
  );
}
