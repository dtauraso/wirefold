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
  // Subscribe reactively so GraphNode re-renders when Go streams node-geometry for
  // this node (portDir / nodeWorldPos / nodeRadius all call getNodeGeometry internally;
  // without this the component never re-renders after the Go stream arrives and ports
  // stay at their default side/slot fallback positions).
  useNodeGeometryStore((s) => s.geoms[node.id]);
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
      <mesh userData={{ nodeId: node.id, body: true }}>
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
// SphereRing — first-cut "show the sphere" visualization.
// When showSphere is on AND a node is selected, draws a thin see-through torus
// ring centered on that node, in the XY plane, with major radius R = distance to
// the node's reference neighbor (its primary OUTPUT-edge target; fallback: first
// connected neighbor). Styled like the node's border ring (NODE_DEFS stroke), but
// transparent + depthWrite false + raycast disabled so it's purely decorative and
// you can see the nodes inside. Re-derives R + center every frame from live geometry.
// ---------------------------------------------------------------------------

function referenceNeighborId(
  nodeId: string,
  edges: RFEdge<EdgeData>[],
): string | null {
  // Primary output edge: first edge whose source is this node.
  const out = edges.find((e) => e.source === nodeId);
  if (out) return out.target;
  // Fallback: first edge touching this node (any direction).
  const any = edges.find((e) => e.source === nodeId || e.target === nodeId);
  if (any) return any.source === nodeId ? any.target : any.source;
  return null;
}

export function SphereRing({
  nodes,
  edges,
  selectedId,
  selectedSphere,
  showSphere,
}: {
  nodes: RFNode<NodeData>[];
  edges: RFEdge<EdgeData>[];
  selectedId: string | null;
  selectedSphere: string | null;
  showSphere: boolean;
}) {
  // Re-render when Go streams node geometry (centers/radius), so R + center track moves.
  useNodeGeometryStore((s) => s.geoms);

  const selNode = selectedId ? nodes.find((n) => n.id === selectedId) ?? null : null;
  const refId = selNode ? referenceNeighborId(selNode.id, edges) : null;
  const refNode = refId ? nodes.find((n) => n.id === refId) ?? null : null;

  const ringColor = useMemo(
    () => new THREE.Color(selNode?.data?.stroke ?? "#888888"),
    [selNode?.data?.stroke],
  );

  if (!showSphere || !selNode || !refNode) return null;

  const center = nodeWorldPos(selNode);
  const R = center.distanceTo(nodeWorldPos(refNode));
  if (R < 1e-3) return null;

  // Thin tube so it reads as a ring, not a donut — scale to the node's own ring tube.
  const tube = Math.max(0.5, nodeRadius(selNode) * 0.08);

  // Highlighted when THIS sphere surface is the current sphere selection.
  const isSphereSelected = selectedSphere === selNode.id;

  // Two perpendicular great-circle rings so the sphere reads as a sphere:
  // the first lies in XY (torusGeometry's default plane), the second is
  // rotated 90° about X into the XZ plane. Both share radius/tube/material,
  // and both carry the sphere userData so clicking either ring selects the sphere.
  const ringMat = (
    <meshStandardMaterial
      color={ringColor}
      emissive={ringColor}
      emissiveIntensity={isSphereSelected ? 1.2 : 0.25}
      transparent
      opacity={isSphereSelected ? 0.95 : 0.55}
      depthWrite={false}
    />
  );

  return (
    <group position={[center.x, center.y, center.z]}>
      <mesh userData={{ sphereSurface: true, sphereNodeId: selNode.id }}>
        <torusGeometry args={[R, tube, 12, 96]} />
        {ringMat}
      </mesh>
      <mesh
        rotation={[Math.PI / 2, 0, 0]}
        userData={{ sphereSurface: true, sphereNodeId: selNode.id }}
      >
        <torusGeometry args={[R, tube, 12, 96]} />
        {ringMat}
      </mesh>
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

// PulseBead: draws the in-flight beads ON a wire at their Go-owned fractional progress.
// A wire may carry MULTIPLE beads at once (a clock-paced train): getPulseMap() is keyed
// by `${edgeId}:${beadID}`, so each in-flight bead on this edge is its own map entry.
// Go owns the clock and each bead's fraction; this component PLOTS ONLY — each frame it
// reads every map entry whose edgeId === this edge and places one sprite at
// lerp(start, end, frac) on the SAME segment SingleEdgeTube draws (Go's wireSegment from
// useEdgeGeometryStore). Because beads and tube share one segment source, every bead is
// provably on the line and rides the wire as the node moves — no lag, no drift. No curve
// sampling beyond the linear lerp, no clock, no delivery message (MODEL.md).
//
// Imperative pool: PULSE_POOL bead groups are mounted once; each frame we map the edge's
// live entries onto pool slots (placed/colored/visible) and hide the unused slots. No
// React state per bead — placement is pure useFrame mutation, off React's render path.
const PULSE_POOL = 16;

export function PulseBead({
  edgeId,
}: {
  edgeId: string;
}) {
  const slotRefs = useRef<(THREE.Group | null)[]>([]);
  const sphereMatRefs = useRef<(THREE.MeshStandardMaterial | null)[]>([]);
  const torusMatRefs = useRef<(THREE.MeshStandardMaterial | null)[]>([]);
  // SAME source SingleEdgeTube subscribes to — beads and tube cannot diverge.
  const seg = useEdgeGeometryStore((s) => s.segments[edgeId]);

  useFrame(() => {
    const slots = slotRefs.current;
    // Gather this edge's live beads (one map entry per in-flight bead on this wire).
    // No segment yet (startup race) → hide all, no crash.
    let slot = 0;
    if (seg) {
      for (const pulse of getPulseMap().values()) {
        if (pulse.edgeId !== edgeId) continue;
        if (slot >= PULSE_POOL) break; // pool exhausted — extra beads wait a frame
        const g = slots[slot];
        if (!g) { slot++; continue; }
        // A non-0/1 value has no style → leave this slot hidden.
        const style = beadStyleForValue(pulse.value);
        if (!style) { g.visible = false; slot++; continue; }
        // Place at the Go-supplied fraction along the Go-supplied segment.
        const f = pulse.frac;
        g.position.set(
          seg.start.x + f * (seg.end.x - seg.start.x),
          seg.start.y + f * (seg.end.y - seg.start.y),
          seg.start.z + f * (seg.end.z - seg.start.z),
        );
        sphereMatRefs.current[slot]?.color.set(style.fill);
        torusMatRefs.current[slot]?.color.set(style.ring);
        g.visible = true;
        slot++;
      }
    }
    // Hide any pool slots not claimed this frame.
    for (let i = slot; i < PULSE_POOL; i++) {
      const g = slots[i];
      if (g) g.visible = false;
    }
  });

  return (
    <>
      {Array.from({ length: PULSE_POOL }, (_, i) => (
        <group key={i} ref={(el) => { slotRefs.current[i] = el; }} visible={false}>
          <mesh raycast={() => null}>
            <sphereGeometry args={[4, 16, 16]} />
            <meshStandardMaterial ref={(el) => { sphereMatRefs.current[i] = el; }} emissiveIntensity={0} />
          </mesh>
          <mesh raycast={() => null}>
            <torusGeometry args={[4, 4 * 0.12, 8, 24]} />
            <meshStandardMaterial ref={(el) => { torusMatRefs.current[i] = el; }} emissiveIntensity={0} />
          </mesh>
        </group>
      ))}
    </>
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

// Arrowhead cone dims — visibly larger than the 1.5 tube radius. Tunable.
const ARROW_HEIGHT = 6;
const ARROW_RADIUS = 3;

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
  const { tubeGeo, haloGeo, arrow } = useMemo(() => {
    if (!seg)
      return {
        tubeGeo: null as THREE.TubeGeometry | null,
        haloGeo: null as THREE.TubeGeometry | null,
        arrow: null as { center: THREE.Vector3; q: THREE.Quaternion } | null,
      };
    const start = new THREE.Vector3(seg.start.x, seg.start.y, seg.start.z);
    const end = new THREE.Vector3(seg.end.x, seg.end.y, seg.end.z);
    // Wire is a straight line: P(t) = Start + t*(End-Start).
    const tubeCurve = new THREE.LineCurve3(start, end);
    const _tubeGeo = new THREE.TubeGeometry(tubeCurve, 1, 1.5, 6, false);
    // Halo: concentric tube on the same segment, larger radius — reads as a glow around the core.
    const _haloGeo = new THREE.TubeGeometry(tubeCurve, 1, 5, 6, false);
    // Directional arrowhead: a cone whose apex sits at the target end (seg.end),
    // base back along the edge. Degenerate (zero-length) segments get no arrow.
    const dir = end.clone().sub(start);
    let _arrow: { center: THREE.Vector3; q: THREE.Quaternion } | null = null;
    if (dir.length() >= 1e-6) {
      dir.normalize();
      // ConeGeometry's apex points +Y; rotate +Y onto the edge direction.
      const q = new THREE.Quaternion().setFromUnitVectors(new THREE.Vector3(0, 1, 0), dir);
      // Place center so apex (= center + dir*height/2) lands on seg.end.
      const center = end.clone().addScaledVector(dir, -ARROW_HEIGHT / 2);
      _arrow = { center, q };
    }
    return { tubeGeo: _tubeGeo, haloGeo: _haloGeo, arrow: _arrow };
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
      {/* Directional arrowhead: cone apex at the target end, fades with the tube. */}
      {arrow && (
        <mesh
          position={[arrow.center.x, arrow.center.y, arrow.center.z]}
          quaternion={[arrow.q.x, arrow.q.y, arrow.q.z, arrow.q.w]}
          raycast={() => null}
        >
          <coneGeometry args={[ARROW_RADIUS, ARROW_HEIGHT, 16]} />
          <meshStandardMaterial
            key={faded ? "faded" : "solid"}
            color={SHADING_PARAM_TUBE_COLOR}
            emissive={new THREE.Color(SHADING_PARAM_TUBE_EMISSIVE)}
            emissiveIntensity={SHADING_PARAM_TUBE_EMISSIVE_INTENSITY}
            transparent={faded}
            opacity={faded ? SHADING_PARAM_NODE_FADE_OPACITY : 1}
          />
        </mesh>
      )}
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
        // A port only wins the pointer when the click lands on its EXPOSED region,
        // not merely anywhere the ray grazes a port sphere. Port spheres sit on the
        // node's ring (radius ~= node radius), so the ray into a node body almost
        // always also clips a connected port; returning that port unconditionally
        // (the old behavior) hijacked every node-body grab into a port-slide and is
        // the real reason nodes were "not draggable". Resolve with the SAME body-
        // proximity precedence the default pick uses: a port wins only if its hit is
        // at/closer than the nearest node-body hit, within PORT_HIT_TOL. Otherwise
        // the body is the intended target → return null so the caller falls through
        // to the node-drag path. (Reducing lattice spacing only enlarged the relative
        // body target enough to sometimes clear ports — a symptom mask, not the fix.)
        const PORT_HIT_TOL = 8;
        let portHit: { id: string; dist: number } | null = null;
        let nodeHitDist: number | null = null;
        for (const hit of hits) {
          const hitObj = hit.object as THREE.Mesh;
          if (!portHit && hitObj.userData?.portId) {
            portHit = { id: hitObj.userData.portId as string, dist: hit.distance };
            continue;
          }
          if (nodeHitDist === null) {
            const hitPoint = hitObj.parent;
            if (!hitPoint) continue;
            for (const n of nodes) {
              const wp = nodeWorldPos(n);
              if (
                Math.abs(hitPoint.position.x - wp.x) < 1 &&
                Math.abs(hitPoint.position.y - wp.y) < 1
              ) {
                nodeHitDist = hit.distance;
                break;
              }
            }
          }
        }
        if (portHit && (nodeHitDist === null || portHit.dist <= nodeHitDist + PORT_HIT_TOL)) {
          return portHit.id;
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
          if (hitObj.userData?.body === true && hitObj.userData?.nodeId) {
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
      // Sphere-surface torus rim hit → encoded "sphere:<nodeId>". Thin rim, so this
      // only fires when the ray actually grazes the ring; clicking through the open
      // middle still passes to the node body inside.
      let sphereHit: { id: string; dist: number } | null = null;

      for (const hit of hits) {
        const hitObj = hit.object as THREE.Mesh;
        if (!sphereHit && hitObj.userData?.sphereSurface) {
          sphereHit = {
            id: "sphere:" + (hitObj.userData.sphereNodeId as string),
            dist: hit.distance,
          };
          continue;
        }
        if (!portHit && hitObj.userData?.portId) {
          portHit = { id: hitObj.userData.portId as string, dist: hit.distance };
          continue;
        }
        if (!edgeHit && hitObj.userData?.edgeId) {
          edgeHit = { id: hitObj.userData.edgeId as string, dist: hit.distance };
          continue;
        }
        if (!nodeHit) {
          // Resolve a node-body hit to its node id. Prefer the explicit
          // userData.nodeId on the body sphere (exact, z-aware: the NEAREST body
          // sphere the ray hits wins). Fall back to x,y proximity only for older
          // meshes without the tag. The old x,y-only scan returned whichever node
          // appeared first in `nodes` sharing the same x,y, so a node directly
          // BEHIND another (same screen x,y, deeper z) could hijack the pick.
          if (hitObj.userData?.body === true && hitObj.userData?.nodeId) {
            nodeHit = { id: hitObj.userData.nodeId as string, dist: hit.distance };
          } else {
            const hitPoint = hitObj.parent;
            if (hitPoint) {
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
        }
      }

      // Node-favoring margin: the wide invisible edge selection-halo runs node→node,
      // so over a node the halo surface is at/closer than the node sphere. Without a
      // bias the edge would win and node-drag would silently break. An edge only wins
      // when it is clearly closer than the node by this margin (world units).
      const EDGE_BIAS = 2;

      // Precedence: port wins over node if within tolerance (covers embedded half of port sphere).
      let picked: string | null = null;
      // Sphere-surface rim wins when its hit is nearer than the node body (the ray
      // actually struck the thin ring rim before reaching the node inside). It does
      // not override a port (ports sit on the node ring and are the finer target).
      if (
        sphereHit &&
        !portHit &&
        (!nodeHit || sphereHit.dist <= nodeHit.dist) &&
        (!edgeHit || sphereHit.dist <= edgeHit.dist)
      ) {
        picked = sphereHit.id;
      } else if (portHit && (!nodeHit || portHit.dist <= nodeHit.dist + PORT_HIT_TOL)) {
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
  selectedSphere,
  hoveredId,
  cameraRef,
  initialCamera3d,
  onPickRequest,
  onPositions,
  onNearestN,
  onCameraSettle,
  showSphere,
}: {
  nodes: RFNode<NodeData>[];
  edges: RFEdge<EdgeData>[];
  selectedId: string | null;
  selectedSphere: string | null;
  hoveredId: string | null;
  showSphere: boolean;
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
      <SphereRing
        nodes={nodes}
        edges={edges}
        selectedId={selectedId}
        selectedSphere={selectedSphere}
        showSphere={showSphere}
      />
    </ProceduralEnvProvider>
  );
}
