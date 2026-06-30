// scene-graph.tsx — GraphNode, SphereRing, SingleEdgeTube, GraphEdges.
import React, { useMemo, useContext } from "react";
import { useFrame } from "@react-three/fiber";
import * as THREE from "three";
import type { RFNode, RFEdge, NodeData, EdgeData } from "../types";
import { EnvTexContext } from "./scene-env";
import { useNodeGeometryStore, getNodeGeometry } from "./node-geometry";
import { nodeRadius, nodeWorldPos, portDir } from "./geometry-helpers";
import { useEdgeGeometryStore } from "./edge-geometry";
import { PulseBead, InteriorBeads } from "./scene-beads";
import { useCameraStore } from "./camera-store";
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
  SHADING_PARAM_TUBE_COLOR,
  SHADING_PARAM_TUBE_EMISSIVE,
  SHADING_PARAM_TUBE_EMISSIVE_INTENSITY,
  SHADING_PARAM_DOUBLE_LINKS_COLOR,
  SHADING_PARAM_DOUBLE_LINKS_EMISSIVE,
  SHADING_PARAM_DOUBLE_LINKS_EMISSIVE_INTENSITY,
} from "../../schema/shading-params";

// ---------------------------------------------------------------------------
// Module-level color constants (hoisted from inline render paths).
// ---------------------------------------------------------------------------

const TUBE_EMISSIVE_COLOR = new THREE.Color(SHADING_PARAM_TUBE_EMISSIVE);
const DOUBLE_LINKS_EMISSIVE_COLOR = new THREE.Color(SHADING_PARAM_DOUBLE_LINKS_EMISSIVE);

// ---------------------------------------------------------------------------
// Single node mesh: sphere + border ring
// ---------------------------------------------------------------------------

// Renders one port sphere (input or output) on a node's surface.
function PortSphere({
  node,
  port,
  isInput,
  selectedId,
  hoveredId,
  strokeColor,
  r,
}: {
  node: RFNode<NodeData>;
  port: { name: string };
  isInput: boolean;
  selectedId?: string | null;
  hoveredId?: string | null;
  strokeColor: THREE.Color;
  r: number;
}) {
  const dir = portDir(node, port.name, isInput);
  if (!dir) return null;
  const portId = `${node.id}:${isInput ? "in" : "out"}:${port.name}`;
  const isSel = selectedId === portId;
  const isHov = hoveredId === portId;
  return (
    <mesh
      position={[dir.x * r, dir.y * r, dir.z * r]}
      scale={isSel ? 1.5 : isHov ? 1.3 : 1}
      userData={{ portId, nodeId: node.id, portName: port.name, isInput, port: true }}
    >
      <sphereGeometry args={[4, 8, 8]} />
      <meshStandardMaterial
        color={isSel ? "#ffcc00" : isHov ? "#aaddff" : strokeColor}
        emissive={isSel ? "#ffcc00" : isHov ? "#aaddff" : "#000000"}
        emissiveIntensity={isSel ? 0.7 : isHov ? 0.4 : 0}
      />
    </mesh>
  );
}

export function GraphNode({
  node,
  selected,
  hovered,
  faded,
  selectedId,
  hoveredId,
  onSphereSurface,
}: {
  node: RFNode<NodeData>;
  selected: boolean;
  hovered: boolean;
  faded: boolean;
  selectedId?: string | null;
  hoveredId?: string | null;
  onSphereSurface?: boolean;
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
  // Sphere-surface nodes get the SAME highlight as the center (selected) node:
  // stroke #ffcc00 + a thicker ring, no extra glow.
  const strokeHex = (selected || onSphereSurface) ? "#ffcc00"
    : hovered ? "#aaddff"
    : (node.data?.stroke ?? "#888888");

  // Memoize THREE.Color objects to avoid allocating on every render.
  const fillColor = useMemo(() => new THREE.Color(fillHex), [fillHex]);
  const strokeColor = useMemo(() => new THREE.Color(strokeHex), [strokeHex]);
  const emissiveFill = useMemo(() => new THREE.Color(0x000000), []);
  const emissiveStroke = useMemo(() => new THREE.Color(0x000000), []);

  const torusThick = (selected || hovered || onSphereSurface) ? r * 0.14 : r * 0.08;
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
          opacity={(selected || onSphereSurface) ? 0.5 : 0}
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
      {(node.data?.inputs ?? []).map((port) => (
        <PortSphere
          key={`in:${port.name}`}
          node={node}
          port={port}
          isInput={true}
          selectedId={selectedId}
          hoveredId={hoveredId}
          strokeColor={strokeColor}
          r={r}
        />
      ))}
      {(node.data?.outputs ?? []).map((port) => (
        <PortSphere
          key={`out:${port.name}`}
          node={node}
          port={port}
          isInput={false}
          selectedId={selectedId}
          hoveredId={hoveredId}
          strokeColor={strokeColor}
          r={r}
        />
      ))}
    </group>
  );
}

// ---------------------------------------------------------------------------
// SphereRing — "show the sphere" visualization for one owner node.
// Draws two thin see-through torus rings (XY + XZ planes) centered on the owner node
// with major radius R = the node's
// Go-streamed sphere radius (nodeRadius reads geoms[id].radius; falls back to local
// compute pre-emit). Styled like the node's border ring (NODE_DEFS stroke), but
// transparent + depthWrite false + raycast disabled so it's purely decorative and
// you can see the nodes inside. Re-derives R + center every frame from live geometry.
// ---------------------------------------------------------------------------

export function SphereRing({
  nodes,
  edges,
  ownerId,
}: {
  nodes: RFNode<NodeData>[];
  edges: RFEdge<EdgeData>[];
  ownerId: string | null; // the node whose sphere to draw (a sphere the selection sits on)
}) {
  // Re-render when Go streams node geometry (centers/radius), so R + center track moves.
  useNodeGeometryStore((s) => s.geoms);

  const ownerNode = ownerId ? nodes.find((n) => n.id === ownerId) ?? null : null;

  // Only output-bearing nodes center a sphere; output-less nodes (no outgoing edge)
  // live on others' surfaces and have no sphere of their own.
  const centersSphere = ownerNode != null && edges.some((e) => e.source === ownerNode.id);

  const ringColor = useMemo(
    () => new THREE.Color(ownerNode?.data?.stroke ?? "#888888"),
    [ownerNode?.data?.stroke],
  );

  if (!ownerNode || !centersSphere) return null;

  const center = nodeWorldPos(ownerNode);
  // Radius: Go streams the REACH radius in sphereR — the max distance from this owner's
  // center to any node it outputs to (its surface children), computed from the resolved
  // centers. This reaches every surface node even when a child was placed by a different
  // parent (e.g. node 5 on 6's sphere; the anchor reached via a feedback edge). No
  // geometry math here — Go owns it. Fall back to the node radius before geometry arrives.
  const geom = getNodeGeometry(ownerNode.id);
  const R = geom?.sphereR ?? nodeRadius(ownerNode);
  if (R < 1e-3) return null;

  // Thin tube so it reads as a ring, not a donut.
  const tube = Math.max(0.5, nodeRadius(ownerNode) * 0.08);

  // Go streams the two great-circle ring normals (vrx/vry/vrz = vertical,
  // frx/fry/frz = flat). Orient each torus so its default +Z axis aligns
  // with the emitted normal via quaternion; fall back to hardcoded XY / XZ
  // orientation if the geometry hasn't arrived yet.
  // torusGeometry lies in the XY plane by default, so its plane normal = +Z (0,0,1).
  const torusDefaultNormal = new THREE.Vector3(0, 0, 1);
  const vrNormal = geom
    ? new THREE.Vector3(geom.vrx, geom.vry, geom.vrz).normalize()
    : new THREE.Vector3(0, 0, 1); // fallback: XY plane (vertical ring)
  const frNormal = geom
    ? new THREE.Vector3(geom.frx, geom.fry, geom.frz).normalize()
    : new THREE.Vector3(1, 0, 0); // fallback: rotate 90° about X into XZ plane
  const vrQ = new THREE.Quaternion().setFromUnitVectors(torusDefaultNormal, vrNormal);
  const frQ = new THREE.Quaternion().setFromUnitVectors(torusDefaultNormal, frNormal);

  const ringMat = (
    <meshStandardMaterial
      color={ringColor}
      emissive={ringColor}
      emissiveIntensity={0.25}
      transparent
      opacity={0.55}
      depthWrite={false}
    />
  );

  // The tori are visual-only: raycast disabled so they never intercept a pick
  // (clicks pass through to the nodes behind them). The sphere is not selectable.
  const noRaycast = () => null;
  return (
    <group position={[center.x, center.y, center.z]}>
      <mesh quaternion={vrQ} raycast={noRaycast}>
        <torusGeometry args={[R, tube, 12, 96]} />
        {ringMat}
      </mesh>
      <mesh quaternion={frQ} raycast={noRaycast}>
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

// Arrowhead cone dims — visibly larger than the 1.5 tube radius. Tunable.
const ARROWHEAD_LENGTH = 6;
const ARROWHEAD_RADIUS = 3;

// Arrowhead cone dims for the double-link overlay (slightly larger than the tube arrows).
const DL_ARROWHEAD_LENGTH = 7;
const DL_ARROWHEAD_RADIUS = 3.5;

/**
 * Builds an arrow descriptor: a cone whose apex sits at `apex`, pointing in `dir`
 * (normalized, toward the apex). `height` is the cone length.
 * ConeGeometry apex is at +Y; we rotate +Y onto `dir`.
 * Returns { center, q } where center places the cone so its apex lands at `apex`.
 */
function buildArrow(
  apex: THREE.Vector3,
  dir: THREE.Vector3,
  height: number,
): { center: THREE.Vector3; q: THREE.Quaternion } {
  const q = new THREE.Quaternion().setFromUnitVectors(new THREE.Vector3(0, 1, 0), dir);
  // apex = center + dir*(height/2) → center = apex - dir*(height/2)
  const center = apex.clone().addScaledVector(dir, -height / 2);
  return { center, q };
}

/**
 * Subscribe to one edge's segment from the edge-geometry store and derive a stable
 * string key over the endpoints.  The key is the useMemo dependency for both
 * SingleEdgeTube and DoubleEdgeOverlay: the memo re-runs only when Go re-streams
 * the segment (e.g. after a node drag), not on every render.
 */
function useEdgeSegment(edgeId: string) {
  const seg = useEdgeGeometryStore((s) => s.segments[edgeId]);
  const segKey = seg
    ? `${seg.start.x},${seg.start.y},${seg.start.z}:${seg.end.x},${seg.end.y},${seg.end.z}`
    : "";
  return { seg, segKey };
}

export function SingleEdgeTube({ edgeId, faded, selected, dimmed }: { edgeId: string; faded: boolean; selected: boolean; dimmed?: boolean }) {
  // Go is the authoritative holder of this edge's segment (Phase 3, MODEL.md). It
  // streams the endpoints (geometry trace) on load and on every node-move;
  // pump.ts writes them to the edge-geometry store. We subscribe to THIS edge's
  // endpoints and draw the tube from them — TS computes no geometry. A dragged node
  // re-streams its touched edges' segments, so the wire follows ~1 frame behind.
  const { seg, segKey } = useEdgeSegment(edgeId);
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
      _arrow = buildArrow(end, dir, ARROWHEAD_LENGTH);
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
          key={faded ? "faded" : dimmed ? "dimmed" : "solid"}
          color={SHADING_PARAM_TUBE_COLOR}
          emissive={TUBE_EMISSIVE_COLOR}
          emissiveIntensity={SHADING_PARAM_TUBE_EMISSIVE_INTENSITY}
          transparent={faded || !!dimmed}
          opacity={faded ? SHADING_PARAM_NODE_FADE_OPACITY : dimmed ? 0.25 : 1}
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
          <coneGeometry args={[ARROWHEAD_RADIUS, ARROWHEAD_LENGTH, 16]} />
          <meshStandardMaterial
            key={faded ? "faded" : dimmed ? "dimmed" : "solid"}
            color={SHADING_PARAM_TUBE_COLOR}
            emissive={TUBE_EMISSIVE_COLOR}
            emissiveIntensity={SHADING_PARAM_TUBE_EMISSIVE_INTENSITY}
            transparent={faded || !!dimmed}
            opacity={faded ? SHADING_PARAM_NODE_FADE_OPACITY : dimmed ? 0.25 : 1}
          />
        </mesh>
      )}
      {/* Pulse bead: plotted at Go's streamed position (Phase 2). */}
      {!faded && <PulseBead edgeId={edgeId} />}
    </>
  );
}

/** Renders a single bidirectional cyan line overlay for one edge when double-links is ON.
 * Reads the same segment from the edge-geometry store as SingleEdgeTube so it lines up
 * exactly with the ports. Draws a thin tube line + two arrowheads pointing outward. */
function DoubleEdgeOverlay({ edgeId }: { edgeId: string }) {
  const { seg, segKey } = useEdgeSegment(edgeId);
  const { lineGeo, arrowStart, arrowEnd } = useMemo(() => {
    if (!seg)
      return {
        lineGeo: null as THREE.TubeGeometry | null,
        arrowStart: null as { center: THREE.Vector3; q: THREE.Quaternion } | null,
        arrowEnd: null as { center: THREE.Vector3; q: THREE.Quaternion } | null,
      };
    const start = new THREE.Vector3(seg.start.x, seg.start.y, seg.start.z);
    const end = new THREE.Vector3(seg.end.x, seg.end.y, seg.end.z);
    const lineCurve = new THREE.LineCurve3(start, end);
    const _lineGeo = new THREE.TubeGeometry(lineCurve, 1, 1.0, 6, false);
    const dir = end.clone().sub(start);
    let _arrowStart: { center: THREE.Vector3; q: THREE.Quaternion } | null = null;
    let _arrowEnd: { center: THREE.Vector3; q: THREE.Quaternion } | null = null;
    if (dir.length() >= 1e-6) {
      const dirNorm = dir.clone().normalize();
      const dirNeg = dirNorm.clone().negate();
      // Arrow at start: cone pointing toward start (apex at start, base inward).
      _arrowStart = buildArrow(start, dirNeg, DL_ARROWHEAD_LENGTH);
      // Arrow at end: cone pointing toward end (apex at end, base inward).
      _arrowEnd = buildArrow(end, dirNorm, DL_ARROWHEAD_LENGTH);
    }
    return { lineGeo: _lineGeo, arrowStart: _arrowStart, arrowEnd: _arrowEnd };
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [segKey]);

  if (!lineGeo) return null;
  return (
    <>
      <mesh geometry={lineGeo} raycast={() => null}>
        <meshStandardMaterial
          color={SHADING_PARAM_DOUBLE_LINKS_COLOR}
          emissive={DOUBLE_LINKS_EMISSIVE_COLOR}
          emissiveIntensity={SHADING_PARAM_DOUBLE_LINKS_EMISSIVE_INTENSITY}
          transparent={false}
        />
      </mesh>
      {arrowStart && (
        <mesh
          position={[arrowStart.center.x, arrowStart.center.y, arrowStart.center.z]}
          quaternion={[arrowStart.q.x, arrowStart.q.y, arrowStart.q.z, arrowStart.q.w]}
          raycast={() => null}
        >
          <coneGeometry args={[DL_ARROWHEAD_RADIUS, DL_ARROWHEAD_LENGTH, 16]} />
          <meshStandardMaterial
            color={SHADING_PARAM_DOUBLE_LINKS_COLOR}
            emissive={DOUBLE_LINKS_EMISSIVE_COLOR}
            emissiveIntensity={SHADING_PARAM_DOUBLE_LINKS_EMISSIVE_INTENSITY}
          />
        </mesh>
      )}
      {arrowEnd && (
        <mesh
          position={[arrowEnd.center.x, arrowEnd.center.y, arrowEnd.center.z]}
          quaternion={[arrowEnd.q.x, arrowEnd.q.y, arrowEnd.q.z, arrowEnd.q.w]}
          raycast={() => null}
        >
          <coneGeometry args={[DL_ARROWHEAD_RADIUS, DL_ARROWHEAD_LENGTH, 16]} />
          <meshStandardMaterial
            color={SHADING_PARAM_DOUBLE_LINKS_COLOR}
            emissive={DOUBLE_LINKS_EMISSIVE_COLOR}
            emissiveIntensity={SHADING_PARAM_DOUBLE_LINKS_EMISSIVE_INTENSITY}
          />
        </mesh>
      )}
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
  const doubleLinksVisible = useCameraStore((s) => s.doubleLinksVisible);
  const overlaysVisible = useCameraStore((s) => s.overlaysVisible);
  const showDoubleLinks = overlaysVisible && doubleLinksVisible;
  return (
    <>
      {edges.map((e) => {
        // Node-presence gate (a dangling edge with a missing node draws nothing).
        // The wire segment itself is sourced from Go's edge-geometry store inside
        // SingleEdgeTube — Go re-streams it on node-move, so the wire tracks drags.
        const s = nodeMap.get(e.source);
        const t = nodeMap.get(e.target);
        if (!s || !t) return null;
        return (
          <React.Fragment key={e.id}>
            <SingleEdgeTube edgeId={e.id} faded={!!e.data?.faded} selected={e.id === selectedId} dimmed={showDoubleLinks} />
            {showDoubleLinks && <DoubleEdgeOverlay edgeId={e.id} />}
          </React.Fragment>
        );
      })}
    </>
  );
}
