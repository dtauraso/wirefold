// buffer-scene.tsx — Phase 4: buffer-driven render path (off by default).
//
// Reads the latest binary snapshot each frame and renders:
//   - Beads: InstancedMesh updated from bead column (positions, live flag).
//   - Nodes: InstancedMesh updated from node column (center positions).
//   - Edges: LineSegments updated from edge column (start/end endpoints).
//
// Mounted ALONGSIDE the existing JSON-trace-driven Scene, NOT instead of it.
// Gated behind USE_BUFFER_RENDER (false by default) so it has zero cost
// until the flip phase switches it to true.
//
// This component does NOT write to any Zustand store. It reads the snapshot
// buffer directly (zero-copy DataView slices via buffer-decode.ts) and fills
// GPU attribute arrays imperatively via useFrame. No domain state flows out.

import React, { useRef, useState, useContext, useMemo, useEffect } from "react";
import { useFrame, useThree } from "@react-three/fiber";
import * as THREE from "three";
import { getLatestSnapshot } from "../snapshot-buffer";
import { USE_NEW_SYSTEM } from "../new-system";
import { decodeSnapshot } from "./buffer-decode";
import { getNavNodeIds, getNavNodeKind } from "./buffer-nav";
import { NODE_DEFS } from "../../schema/node-defs";
import { ndcToPixel } from "./geometry-helpers";
import { anglesToWorldOffset } from "./viewpoint-bridge";
import { EnvTexContext } from "./scene-env";
import { beadStyleForValue } from "./bead-style";
import { INTERIOR_SLOTS_PER_NODE } from "./buffer-decode";
import { surfaceRowSet, ownerRowSet, type EdgeAdj, type SelMode } from "./buffer-surface";
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
  SHADING_PARAM_TUBE_COLOR,
  SHADING_PARAM_TUBE_EMISSIVE,
  SHADING_PARAM_TUBE_EMISSIVE_INTENSITY,
  SHADING_PARAM_DOUBLE_LINKS_COLOR,
  SHADING_PARAM_DOUBLE_LINKS_EMISSIVE,
  SHADING_PARAM_DOUBLE_LINKS_EMISSIVE_INTENSITY,
} from "../../schema/shading-params";
import {
  readBeadX, readBeadY, readBeadZ, readBeadLive, readBeadValue,
  readNodeCX, readNodeCY, readNodeCZ, readNodeRadius, readNodeSelected,
  readNodeTorusRed, readNodeMissVal, readNodeMX, readNodeMY, readNodeMZ,
  readNodeSphereR, readNodeVRX, readNodeVRY, readNodeVRZ, readNodeFRX, readNodeFRY, readNodeFRZ,
  readInteriorPresent, readInteriorValue, readInteriorOX, readInteriorOY, readInteriorOZ,
  readEdgeSX, readEdgeSY, readEdgeSZ, readEdgeEX, readEdgeEY, readEdgeEZ,
  readEdgeSrcNodeRow, readEdgeDstNodeRow,
  readPortNodeRow, readPortDX, readPortDY, readPortDZ,
  readOverlayOverlaysVis, readOverlayDoubleLinks, readOverlaySelMode,
  readCameraPX, readCameraPY, readCameraPZ, readCameraR,
  readCameraPosTheta, readCameraPosPhi, readCameraUpTheta, readCameraUpPhi,
} from "../../schema/buffer-layout";

/** Projected label position, keyed by node id (buffer row → id via the id table). */
export interface BufferLabelPos { id: string; px: number; py: number; cx: number; cy: number; }

// ── Dev flag ──────────────────────────────────────────────────────────────────
// Follows the ONE master switch (USE_NEW_SYSTEM). ON = mount the buffer render path;
// OFF (default) = zero cost, the JSON render path runs unchanged. Toggle at RUNTIME via
// the `wirefold.newSystem` VS Code setting + Reload Window — no source edit needed.
export const USE_BUFFER_RENDER = USE_NEW_SYSTEM;

// ── Sizing constants ──────────────────────────────────────────────────────────
const INITIAL_BEAD_CAP  = 64;
const INITIAL_NODE_CAP  = 32;
const INITIAL_EDGE_CAP  = 32; // edge positions buffer: N edges × 2 endpoints × 3 floats
const INITIAL_PORT_CAP  = 64; // port spheres: one per node port (input + output), grows as needed

const BEAD_SPHERE_RADIUS = 4;
// On-wire (transit) bead ring tube ratio — mirror scene-beads.tsx's PulseBead
// (PULSE_BEAD_R=4, BEAD_RING_TUBE_RATIO=0.12) so the buffer path's transit beads
// match the JSON path's look exactly.
const BEAD_RING_TUBE_RATIO = 0.12;
const NODE_SPHERE_RADIUS = 12;
// Interior (held) bead sphere radius + ring tube ratio — mirror scene-beads.tsx's
// InteriorSlotBead (INTERIOR_BEAD_R=5, BEAD_RING_TUBE_RATIO=0.12) so the buffer path's
// interior beads match the JSON path's look exactly.
const INTERIOR_BEAD_R = 5;
const INTERIOR_RING_TUBE_RATIO = 0.12;

// Fallback fill/stroke for a node whose kind is unknown or whose sidecar message has
// not arrived yet. Neutral grey — matches GraphNode's own defaults
// (fill "#ffffff"/stroke "#888888" ← node.data fallbacks).
const NODE_DEFAULT_FILL = "#ffffff";
const NODE_DEFAULT_STROKE = "#888888";
// userData tag marking the NodeInstances body InstancedMesh as the pickable node
// target under the new system. RaycasterHelper (scene-content.tsx) sees this tag on a
// hit and resolves hit.instanceId → node id via the buffer-nav id table, since the
// buffer-rendered nodes carry no per-node userData.nodeId the old raycast path relies on.
export const BUFFER_NODE_TAG = "bufferNode";
// userData tag marking the PortInstances InstancedMesh as the pickable PORT target under the
// new system. On a hit, RaycasterHelper (scene-content.tsx) reads intersection.instanceId —
// which IS the buffer PORT-ROW index (PortInstances draws ports in buffer row order) — and
// forwards that numeric row to Go, which resolves it back to a (node, port). No port-name
// string is rendered or sent.
export const BUFFER_PORT_TAG = "bufferPort";
// Port hit-sphere radius (world units): the small grabbable ball drawn at each port. Matches
// the pre-branch PortSphere (scene-graph.tsx PORT_SPHERE_R).
const PORT_SPHERE_R = 4;
// Border-ring tube thickness as a fraction of the node radius (mirrors GraphNode's
// resting torusThick = r * 0.08).
const NODE_RING_TUBE_RATIO = 0.08;

/** Resolve a node row's fill/stroke from its Go kind via NODE_DEFS, with a grey fallback. */
export function nodeRowColors(id: string): { fill: string; stroke: string } {
  const kind = getNavNodeKind(id);
  const def = kind ? NODE_DEFS[kind] : undefined;
  return {
    fill: def?.fill ?? NODE_DEFAULT_FILL,
    stroke: def?.stroke ?? NODE_DEFAULT_STROKE,
  };
}

// ── Sub-components (split so capacity growth triggers localised re-render) ────

// On-wire (transit) beads rendered along the wires, matching the JSON path's PulseBead
// (scene-beads.tsx). A bead draws only when Live=1 AND its value has a bead-style (0|1);
// a non-0/1 value has no style and is HIDDEN (excluded from the draw count), exactly like
// PulseBead. Two InstancedMeshes share one useFrame: a sphere body (R=4) and a torus ring
// (R=4, tube 4*0.12), both meshStandardMaterial with emissiveIntensity=0 like PulseBead.
// Color is value-driven via bead-style.ts (fill sphere + ring torus) — the same source the
// JSON on-wire/interior beads use, so buffer and JSON transit beads cannot visually diverge.
function BeadInstances({ capacity }: { capacity: number }) {
  const bodyRef = useRef<THREE.InstancedMesh>(null);
  const ringRef = useRef<THREE.InstancedMesh>(null);
  const matRef  = useRef(new THREE.Matrix4());
  const colRef  = useRef(new THREE.Color());

  useFrame(() => {
    const body = bodyRef.current;
    const ring = ringRef.current;
    if (!body || !ring) return;

    const snap = getLatestSnapshot();
    if (!snap) { body.count = 0; ring.count = 0; return; }
    const decoded = decodeSnapshot(snap);
    if (!decoded) { body.count = 0; ring.count = 0; return; }
    const { beadCount, beadView } = decoded;

    let slot = 0;
    for (let i = 0; i < beadCount && slot < capacity; i++) {
      if (!readBeadLive(beadView, i)) continue;
      const style = beadStyleForValue(readBeadValue(beadView, i));
      if (!style) continue; // non-0/1 value → hide (never paint a fallback)
      matRef.current.setPosition(
        readBeadX(beadView, i),
        readBeadY(beadView, i),
        readBeadZ(beadView, i),
      );
      body.setMatrixAt(slot, matRef.current);
      ring.setMatrixAt(slot, matRef.current);
      body.setColorAt(slot, colRef.current.set(style.fill));
      ring.setColorAt(slot, colRef.current.set(style.ring));
      slot++;
    }
    body.count = slot;
    ring.count = slot;
    body.instanceMatrix.needsUpdate = true;
    ring.instanceMatrix.needsUpdate = true;
    if (body.instanceColor) body.instanceColor.needsUpdate = true;
    if (ring.instanceColor) ring.instanceColor.needsUpdate = true;
  });

  return (
    <>
      <instancedMesh ref={bodyRef} args={[undefined, undefined, capacity]}>
        <sphereGeometry args={[BEAD_SPHERE_RADIUS, 16, 16]} />
        <meshStandardMaterial emissiveIntensity={0} />
      </instancedMesh>
      <instancedMesh ref={ringRef} args={[undefined, undefined, capacity]}>
        <torusGeometry args={[BEAD_SPHERE_RADIUS, BEAD_SPHERE_RADIUS * BEAD_RING_TUBE_RATIO, 8, 24]} />
        <meshStandardMaterial emissiveIntensity={0} />
      </instancedMesh>
    </>
  );
}

// Solid node render matching GraphNode's look: a SOLID sphere per node (fill from
// NODE_DEFS[kind].fill) plus a border torus ring (stroke from NODE_DEFS[kind].stroke).
// Two InstancedMeshes share one useFrame; both use unit geometry scaled per-instance by
// the buffer's node radius, so a node's world size matches the JSON path. Per-node fill/
// stroke is driven via instanceColor (setColorAt). Kind→color is a pure NODE_DEFS lookup
// keyed by the row-aligned id table (buffer-nav) — no color travels in the buffer.
function NodeInstances({ capacity }: { capacity: number }) {
  const envTex = useContext(EnvTexContext);
  const bodyRef = useRef<THREE.InstancedMesh>(null);
  const ringRef = useRef<THREE.InstancedMesh>(null);
  const matRef  = useRef(new THREE.Matrix4());
  const posRef  = useRef(new THREE.Vector3());
  const quatRef = useRef(new THREE.Quaternion());
  const sclRef  = useRef(new THREE.Vector3());
  const colRef  = useRef(new THREE.Color());

  useFrame(() => {
    const body = bodyRef.current;
    const ring = ringRef.current;
    if (!body || !ring) return;

    const snap = getLatestSnapshot();
    if (!snap) { body.count = 0; ring.count = 0; return; }
    const decoded = decodeSnapshot(snap);
    if (!decoded) { body.count = 0; ring.count = 0; return; }
    const { nodeCount, nodeView } = decoded;
    const ids = getNavNodeIds();

    const n = Math.min(nodeCount, capacity);
    const q = quatRef.current; // identity (no per-node rotation)
    for (let i = 0; i < n; i++) {
      const r = readNodeRadius(nodeView, i) || NODE_SPHERE_RADIUS;
      posRef.current.set(
        readNodeCX(nodeView, i),
        readNodeCY(nodeView, i),
        readNodeCZ(nodeView, i),
      );
      // Body: unit sphere scaled to the node radius.
      sclRef.current.setScalar(r);
      matRef.current.compose(posRef.current, q, sclRef.current);
      body.setMatrixAt(i, matRef.current);
      // Ring: unit torus (major radius 1) scaled to the node radius; tube thickness
      // is baked into the geometry as a fraction of that radius (NODE_RING_TUBE_RATIO).
      ring.setMatrixAt(i, matRef.current);

      const { fill, stroke } = nodeRowColors(ids[i] ?? `#${i}`);
      body.setColorAt(i, colRef.current.set(fill));
      ring.setColorAt(i, colRef.current.set(stroke));
    }
    body.count = n;
    ring.count = n;
    body.instanceMatrix.needsUpdate = true;
    ring.instanceMatrix.needsUpdate = true;
    if (body.instanceColor) body.instanceColor.needsUpdate = true;
    if (ring.instanceColor) ring.instanceColor.needsUpdate = true;
    // Refresh the InstancedMesh bounding sphere so raycast picking stays accurate as
    // nodes move (three.js early-outs a ray against a cached union sphere; a dragged
    // node outside the stale sphere would otherwise be un-pickable). Cheap for the
    // small node counts here.
    body.computeBoundingSphere();
  });

  return (
    <>
      <instancedMesh ref={bodyRef} args={[undefined, undefined, capacity]} userData={{ [BUFFER_NODE_TAG]: true }}>
        <sphereGeometry args={[1, 16, 16]} />
        {/* Match GraphNode's glassy translucent body EXACTLY (scene-graph.tsx): a
            meshPhysicalMaterial with transmission + depthWrite=false + opacity 0.92 so
            the node interior (held/interior beads) shows through. Per-node fill is the
            instanceColor (setColorAt below); the shared material color stays white so
            instanceColor is applied verbatim. envMap comes from the same PMREM context
            the JSON path uses (BufferScene is wrapped in ProceduralEnvProvider). */}
        <meshPhysicalMaterial
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
          opacity={SHADING_PARAM_NODE_OPACITY}
          depthWrite={false}
        />
      </instancedMesh>
      <instancedMesh ref={ringRef} args={[undefined, undefined, capacity]}>
        <torusGeometry args={[1, NODE_RING_TUBE_RATIO, 8, 32]} />
        <meshStandardMaterial roughness={0.6} metalness={0} />
      </instancedMesh>
    </>
  );
}

// Port spheres: one small grabbable ball per buffer PORT row, matching the pre-branch
// PortSphere (scene-graph.tsx). Placement mirrors PortSphere exactly: at nodeCenter +
// portDir*nodeRadius, where nodeCenter/nodeRadius come from the owning node's row (the port's
// NodeRow column) and portDir is the port's DX/DY/DZ surface direction. Color is the owning
// node's stroke (the same NODE_DEFS[kind].stroke NodeInstances uses for its ring). One
// InstancedMesh for all ports, tagged BUFFER_PORT_TAG for picking — instance i IS buffer port
// row i, so a raycast hit's instanceId is the port row Go resolves to a (node, port). No port
// position or identity is computed beyond this render placement; the numeric buffer owns it.
function PortInstances({ capacity }: { capacity: number }) {
  const meshRef = useRef<THREE.InstancedMesh>(null);
  const matRef  = useRef(new THREE.Matrix4());
  const posRef  = useRef(new THREE.Vector3());
  const quatRef = useRef(new THREE.Quaternion());
  const sclRef  = useRef(new THREE.Vector3());
  const colRef  = useRef(new THREE.Color());

  useFrame(() => {
    const mesh = meshRef.current;
    if (!mesh) return;

    const snap = getLatestSnapshot();
    if (!snap) { mesh.count = 0; return; }
    const decoded = decodeSnapshot(snap);
    if (!decoded) { mesh.count = 0; return; }
    const { portCount, portView, nodeCount, nodeView } = decoded;
    const ids = getNavNodeIds();

    const q = quatRef.current;
    // Instance index MUST stay == buffer port row so a raycast hit's instanceId is the port
    // row Go resolves. Every row 0..portCount-1 is filled; a port whose owning node has not
    // yet streamed is hidden with a zero-scale (degenerate) matrix rather than skipped.
    const n = Math.min(portCount, capacity);
    for (let i = 0; i < n; i++) {
      const nodeRow = readPortNodeRow(portView, i);
      if (nodeRow < 0 || nodeRow >= nodeCount) {
        sclRef.current.setScalar(0); // hide until the owning node resolves
        posRef.current.set(0, 0, 0);
      } else {
        const r = readNodeRadius(nodeView, nodeRow) || NODE_SPHERE_RADIUS;
        sclRef.current.setScalar(1);
        // World placement = node center + surface dir * node radius (pre-branch PortSphere).
        posRef.current.set(
          readNodeCX(nodeView, nodeRow) + readPortDX(portView, i) * r,
          readNodeCY(nodeView, nodeRow) + readPortDY(portView, i) * r,
          readNodeCZ(nodeView, nodeRow) + readPortDZ(portView, i) * r,
        );
        mesh.setColorAt(i, colRef.current.set(nodeRowColors(ids[nodeRow] ?? `#${nodeRow}`).stroke));
      }
      matRef.current.compose(posRef.current, q, sclRef.current);
      mesh.setMatrixAt(i, matRef.current);
    }
    mesh.count = n;
    mesh.instanceMatrix.needsUpdate = true;
    if (mesh.instanceColor) mesh.instanceColor.needsUpdate = true;
    mesh.computeBoundingSphere();
  });

  return (
    <instancedMesh ref={meshRef} args={[undefined, undefined, capacity]} userData={{ [BUFFER_PORT_TAG]: true }}>
      <sphereGeometry args={[PORT_SPHERE_R, 8, 8]} />
      <meshStandardMaterial />
    </instancedMesh>
  );
}

// Highlight drawn around the Go-selected node AND every node ON THE SURFACE of that
// node's sphere (pre-branch parity). Matches the scene-graph.tsx GraphNode look: a thick
// yellow torus ring + an orange halo sphere, identical for the selected node and each
// on-surface node. Go OWNS the selection (the Selected column marks the one selected
// node, SelMode picks the mode); the on-surface set is pure edge-graph topology derived
// here from the Edge block's src/dst node-row adjacency via surfaceRowSet — no TS
// selection state, no geometry/timing logic. A pooled set of highlight groups (one per
// highlighted node) is repositioned each frame; unused pool slots hide.
const HIGHLIGHT_POOL = 32;

function SelectionHighlight({ capacity }: { capacity: number }) {
  const groupRefs = useRef<(THREE.Group | null)[]>([]);
  // Reused scratch across frames so the useFrame allocates nothing steady-state.
  const edgesRef = useRef<EdgeAdj[]>([]);

  useFrame(() => {
    const groups = groupRefs.current;

    const snap = getLatestSnapshot();
    const decoded = snap ? decodeSnapshot(snap) : null;
    let slot = 0;
    if (decoded) {
      const { nodeCount, nodeView, edgeCount, edgeView, overlayView } = decoded;

      // Find the selected row (at most one).
      let selectedRow = -1;
      for (let i = 0; i < nodeCount; i++) {
        if (readNodeSelected(nodeView, i)) { selectedRow = i; break; }
      }

      if (selectedRow >= 0) {
        // Build edge adjacency (node-row src/dst) from the Edge block.
        const edges = edgesRef.current;
        edges.length = 0;
        for (let e = 0; e < edgeCount; e++) {
          edges.push({ src: readEdgeSrcNodeRow(edgeView, e), dst: readEdgeDstNodeRow(edgeView, e) });
        }
        const mode: SelMode = readOverlaySelMode(overlayView) ? "own" : "surface";
        const rows = surfaceRowSet(selectedRow, mode, edges);

        for (const row of rows) {
          if (slot >= HIGHLIGHT_POOL || slot >= capacity) break;
          if (row < 0 || row >= nodeCount) continue;
          const g = groups[slot];
          if (!g) { slot++; continue; }
          const r = readNodeRadius(nodeView, row) || NODE_SPHERE_RADIUS;
          g.position.set(
            readNodeCX(nodeView, row),
            readNodeCY(nodeView, row),
            readNodeCZ(nodeView, row),
          );
          // Scale so child geometries built at radius=1 match r.
          // Torus: args=[1, 0.14, 8, 32]; halo sphere: args=[1.45, 16, 16].
          g.scale.setScalar(r);
          g.visible = true;
          slot++;
        }
      }
    }
    // Hide unused pool slots.
    for (let i = slot; i < HIGHLIGHT_POOL; i++) {
      const g = groups[i];
      if (g) g.visible = false;
    }
  });

  return (
    <>
      {Array.from({ length: HIGHLIGHT_POOL }, (_, i) => (
        <group key={i} ref={(el) => { groupRefs.current[i] = el; }} visible={false}>
          {/* Yellow torus ring — matches GraphNode selected border: r*0.14 thick */}
          <mesh raycast={() => null}>
            <torusGeometry args={[1, 0.14, 8, 32]} />
            <meshStandardMaterial color="#ffcc00" emissive="#ffcc00" emissiveIntensity={0.3} />
          </mesh>
          {/* Orange halo sphere — matches GraphNode NODE_HALO_R_RATIO=1.45 */}
          <mesh raycast={() => null}>
            <sphereGeometry args={[1.45, 16, 16]} />
            <meshBasicMaterial
              color="#ff5a00"
              transparent
              opacity={0.5}
              side={THREE.DoubleSide}
              depthWrite={false}
            />
          </mesh>
        </group>
      ))}
    </>
  );
}

// ── Sphere rings ─────────────────────────────────────────────────────────────────
// "Show the sphere" visualization: for each sphere OWNER of the current selection, two
// thin see-through great-circle tori are drawn AT the owner's center showing the sphere
// boundary. Mirrors the pre-branch SphereRing (scene-graph.tsx) EXACTLY: major radius R =
// the owner's Go-streamed sphereR (buffer SphereR column), tube = max(0.5, radius*0.08),
// two tori oriented by the node's two ring-plane normals (VR vertical, FR flat), material
// = owner stroke color, emissiveIntensity 0.25, opacity 0.55, depthWrite false, raycast
// disabled (purely decorative — clicks pass through to the nodes inside). Owners come from
// ownerRowSet over the Edge-block adjacency; only drawn when a selection exists.
const SPHERE_RING_EMISSIVE_INTENSITY = 0.25;
const SPHERE_RING_OPACITY = 0.55;
const SPHERE_RING_TUBE_RATIO = 0.08; // pre-branch: nodeRadius(owner) * 0.08
const SPHERE_RING_TUBE_MIN = 0.5;
const _sphereRingDefaultNormal = new THREE.Vector3(0, 0, 1); // torusGeometry lies in XY (normal +Z)

interface OwnerRing {
  cx: number; cy: number; cz: number;
  R: number; tube: number;
  vrx: number; vry: number; vrz: number;
  frx: number; fry: number; frz: number;
  color: string;
}

// One owner's pair of great-circle tori. Geometry + orientation quaternions are rebuilt
// only when the owner's R/tube/normals change (keyed useMemo) — not every frame.
function SphereRingBuf({ ring }: { ring: OwnerRing }) {
  const { geo, vrQ, frQ } = useMemo(() => {
    const _geo = new THREE.TorusGeometry(ring.R, ring.tube, 12, 96);
    const vrN = new THREE.Vector3(ring.vrx, ring.vry, ring.vrz);
    if (vrN.lengthSq() < 1e-12) vrN.set(0, 0, 1); else vrN.normalize();
    const frN = new THREE.Vector3(ring.frx, ring.fry, ring.frz);
    if (frN.lengthSq() < 1e-12) frN.set(1, 0, 0); else frN.normalize();
    return {
      geo: _geo,
      vrQ: new THREE.Quaternion().setFromUnitVectors(_sphereRingDefaultNormal, vrN),
      frQ: new THREE.Quaternion().setFromUnitVectors(_sphereRingDefaultNormal, frN),
    };
  }, [ring.R, ring.tube, ring.vrx, ring.vry, ring.vrz, ring.frx, ring.fry, ring.frz]);

  // R3F does not auto-dispose an imperatively-passed geometry; dispose on rebuild/unmount.
  useEffect(() => () => { geo.dispose(); }, [geo]);

  return (
    <group position={[ring.cx, ring.cy, ring.cz]}>
      <mesh geometry={geo} quaternion={[vrQ.x, vrQ.y, vrQ.z, vrQ.w]} raycast={() => null}>
        <meshStandardMaterial
          color={ring.color}
          emissive={ring.color}
          emissiveIntensity={SPHERE_RING_EMISSIVE_INTENSITY}
          transparent
          opacity={SPHERE_RING_OPACITY}
          depthWrite={false}
        />
      </mesh>
      <mesh geometry={geo} quaternion={[frQ.x, frQ.y, frQ.z, frQ.w]} raycast={() => null}>
        <meshStandardMaterial
          color={ring.color}
          emissive={ring.color}
          emissiveIntensity={SPHERE_RING_EMISSIVE_INTENSITY}
          transparent
          opacity={SPHERE_RING_OPACITY}
          depthWrite={false}
        />
      </mesh>
    </group>
  );
}

function SphereRings() {
  const [rings, setRings] = useState<OwnerRing[]>([]);
  const keyRef = useRef<string>("");
  const edgesRef = useRef<EdgeAdj[]>([]);

  useFrame(() => {
    const snap = getLatestSnapshot();
    const decoded = snap ? decodeSnapshot(snap) : null;
    let key = "";
    const next: OwnerRing[] = [];
    if (decoded) {
      const { nodeCount, nodeView, edgeCount, edgeView, overlayView } = decoded;

      // Selected row (at most one).
      let selectedRow = -1;
      for (let i = 0; i < nodeCount; i++) {
        if (readNodeSelected(nodeView, i)) { selectedRow = i; break; }
      }

      if (selectedRow >= 0) {
        const edges = edgesRef.current;
        edges.length = 0;
        for (let e = 0; e < edgeCount; e++) {
          edges.push({ src: readEdgeSrcNodeRow(edgeView, e), dst: readEdgeDstNodeRow(edgeView, e) });
        }
        const mode: SelMode = readOverlaySelMode(overlayView) ? "own" : "surface";
        const ids = getNavNodeIds();
        for (const row of ownerRowSet(selectedRow, mode, edges)) {
          if (row < 0 || row >= nodeCount) continue;
          // R = Go-streamed reach radius (sphereR); fall back to node radius pre-emit.
          const radius = readNodeRadius(nodeView, row) || NODE_SPHERE_RADIUS;
          const R = readNodeSphereR(nodeView, row) || radius;
          if (R < 1e-3) continue;
          const tube = Math.max(SPHERE_RING_TUBE_MIN, radius * SPHERE_RING_TUBE_RATIO);
          const ring: OwnerRing = {
            cx: readNodeCX(nodeView, row), cy: readNodeCY(nodeView, row), cz: readNodeCZ(nodeView, row),
            R, tube,
            vrx: readNodeVRX(nodeView, row), vry: readNodeVRY(nodeView, row), vrz: readNodeVRZ(nodeView, row),
            frx: readNodeFRX(nodeView, row), fry: readNodeFRY(nodeView, row), frz: readNodeFRZ(nodeView, row),
            color: nodeRowColors(ids[row] ?? `#${row}`).stroke,
          };
          next.push(ring);
          key += `${ring.cx},${ring.cy},${ring.cz}|${ring.R},${ring.tube}|${ring.vrx},${ring.vry},${ring.vrz}|${ring.frx},${ring.fry},${ring.frz}|${ring.color};`;
        }
      }
    }
    // Rebuild only when the owner set / geometry / color actually changed.
    if (key !== keyRef.current) {
      keyRef.current = key;
      setRings(next);
    }
  });

  return (
    <>
      {rings.map((ring, i) => (
        <SphereRingBuf key={i} ring={ring} />
      ))}
    </>
  );
}

// Interior (held) beads rendered INSIDE each node, matching the JSON path's
// InteriorBeads (scene-beads.tsx). The Interior block carries a fixed
// INTERIOR_SLOTS_PER_NODE rows per node (row = nodeRow*slots + slot); a slot draws only
// when Present=1 AND its value has a bead-style (0|1). World position = the node's
// buffer center + the Go-owned NODE-LOCAL slot offset (OX/OY/OZ) — the buffer path has
// no node group to inherit, so we add the center here (the JSON path composes it via the
// scene graph). Color is value-driven via bead-style.ts (fill sphere + ring torus), the
// same source the JSON interior/edge beads use, so they cannot visually diverge.
function InteriorBeadInstances({ capacity }: { capacity: number }) {
  const bodyRef = useRef<THREE.InstancedMesh>(null);
  const ringRef = useRef<THREE.InstancedMesh>(null);
  const matRef  = useRef(new THREE.Matrix4());
  const posRef  = useRef(new THREE.Vector3());
  const quatRef = useRef(new THREE.Quaternion());
  const sclRef  = useRef(new THREE.Vector3());
  const colRef  = useRef(new THREE.Color());

  useFrame(() => {
    const body = bodyRef.current;
    const ring = ringRef.current;
    if (!body || !ring) return;

    const snap = getLatestSnapshot();
    if (!snap) { body.count = 0; ring.count = 0; return; }
    const decoded = decodeSnapshot(snap);
    if (!decoded) { body.count = 0; ring.count = 0; return; }
    const { nodeCount, nodeView, interiorView } = decoded;

    const q = quatRef.current; // identity (interior beads carry no rotation)
    sclRef.current.setScalar(INTERIOR_BEAD_R);
    let slot = 0;
    for (let i = 0; i < nodeCount && slot < capacity; i++) {
      const cx = readNodeCX(nodeView, i);
      const cy = readNodeCY(nodeView, i);
      const cz = readNodeCZ(nodeView, i);
      for (let s = 0; s < INTERIOR_SLOTS_PER_NODE && slot < capacity; s++) {
        const row = i * INTERIOR_SLOTS_PER_NODE + s;
        if (!readInteriorPresent(interiorView, row)) continue;
        const style = beadStyleForValue(readInteriorValue(interiorView, row));
        if (!style) continue; // non-0/1 value → hide (never paint a fallback)
        // World = node center + Go-owned node-local slot offset.
        posRef.current.set(
          cx + readInteriorOX(interiorView, row),
          cy + readInteriorOY(interiorView, row),
          cz + readInteriorOZ(interiorView, row),
        );
        matRef.current.compose(posRef.current, q, sclRef.current);
        body.setMatrixAt(slot, matRef.current);
        ring.setMatrixAt(slot, matRef.current);
        body.setColorAt(slot, colRef.current.set(style.fill));
        ring.setColorAt(slot, colRef.current.set(style.ring));
        slot++;
      }
    }
    body.count = slot;
    ring.count = slot;
    body.instanceMatrix.needsUpdate = true;
    ring.instanceMatrix.needsUpdate = true;
    if (body.instanceColor) body.instanceColor.needsUpdate = true;
    if (ring.instanceColor) ring.instanceColor.needsUpdate = true;
  });

  return (
    <>
      {/* Unit-radius geometry scaled per-instance to INTERIOR_BEAD_R; color is
          value-driven via setColorAt (fill sphere + ring torus). */}
      <instancedMesh ref={bodyRef} args={[undefined, undefined, capacity]}>
        <sphereGeometry args={[1, 16, 16]} />
        <meshStandardMaterial />
      </instancedMesh>
      <instancedMesh ref={ringRef} args={[undefined, undefined, capacity]}>
        <torusGeometry args={[1, INTERIOR_RING_TUBE_RATIO, 8, 24]} />
        <meshStandardMaterial />
      </instancedMesh>
    </>
  );
}

// ── Edge tubes + arrowheads ─────────────────────────────────────────────────────
// Real 3D edge render matching the JSON path's SingleEdgeTube / DoubleEdgeOverlay
// (scene-graph.tsx). Endpoints come from the buffer's Edge block (SX..EZ). Edges change
// only on load / node-drag, so we hold the segment set in React state and rebuild the
// per-edge TubeGeometry only when a coordinate actually changes (keyed compare in the
// per-frame poll) — no geometry rebuilt on frames where nothing moved. When double-links
// is ON (OverlaysVis && DoubleLinks in the buffer's Overlay block), the main tubes dim to
// opacity 0.25 and a cyan bidirectional overlay is drawn on the same segment.

// Arrowhead cone dims for the core tube — mirror scene-graph.tsx.
const ARROWHEAD_LENGTH = 6;
const ARROWHEAD_RADIUS = 3;
// Arrowhead cone dims for the double-link overlay (slightly larger than the tube arrows).
const DL_ARROWHEAD_LENGTH = 7;
const DL_ARROWHEAD_RADIUS = 3.5;

const TUBE_EMISSIVE_COLOR = new THREE.Color(SHADING_PARAM_TUBE_EMISSIVE);
const DOUBLE_LINKS_EMISSIVE_COLOR = new THREE.Color(SHADING_PARAM_DOUBLE_LINKS_EMISSIVE);

interface EdgeSeg { sx: number; sy: number; sz: number; ex: number; ey: number; ez: number; }

/**
 * Builds an arrow descriptor: a cone whose apex sits at `apex`, pointing in `dir`
 * (normalized, toward the apex). ConeGeometry apex is at +Y; we rotate +Y onto `dir`.
 * center places the cone so its apex lands at `apex`. Mirrors scene-graph.tsx buildArrow.
 */
function buildArrow(apex: THREE.Vector3, dir: THREE.Vector3, height: number): {
  center: THREE.Vector3; q: THREE.Quaternion;
} {
  const q = new THREE.Quaternion().setFromUnitVectors(new THREE.Vector3(0, 1, 0), dir);
  const center = apex.clone().addScaledVector(dir, -height / 2);
  return { center, q };
}

// One edge's core tube (radius 1.5) + destination arrowhead, mirroring SingleEdgeTube.
// `dimmed` (double-links on) drops opacity to 0.25 like the JSON path.
function EdgeTube({ seg, dimmed }: { seg: EdgeSeg; dimmed: boolean }) {
  const { tubeGeo, arrow } = useMemo(() => {
    const start = new THREE.Vector3(seg.sx, seg.sy, seg.sz);
    const end = new THREE.Vector3(seg.ex, seg.ey, seg.ez);
    const curve = new THREE.LineCurve3(start, end);
    const _tubeGeo = new THREE.TubeGeometry(curve, 1, 1.5, 6, false);
    const dir = end.clone().sub(start);
    let _arrow: { center: THREE.Vector3; q: THREE.Quaternion } | null = null;
    if (dir.length() >= 1e-6) {
      dir.normalize();
      _arrow = buildArrow(end, dir, ARROWHEAD_LENGTH);
    }
    return { tubeGeo: _tubeGeo, arrow: _arrow };
  }, [seg.sx, seg.sy, seg.sz, seg.ex, seg.ey, seg.ez]);

  // R3F does not auto-dispose an imperatively-passed geometry={...}; dispose on rebuild/unmount.
  useEffect(() => () => { tubeGeo.dispose(); }, [tubeGeo]);

  return (
    <>
      <mesh geometry={tubeGeo}>
        <meshStandardMaterial
          key={dimmed ? "dimmed" : "solid"}
          color={SHADING_PARAM_TUBE_COLOR}
          emissive={TUBE_EMISSIVE_COLOR}
          emissiveIntensity={SHADING_PARAM_TUBE_EMISSIVE_INTENSITY}
          transparent={dimmed}
          opacity={dimmed ? 0.25 : 1}
        />
      </mesh>
      {arrow && (
        <mesh
          position={[arrow.center.x, arrow.center.y, arrow.center.z]}
          quaternion={[arrow.q.x, arrow.q.y, arrow.q.z, arrow.q.w]}
          raycast={() => null}
        >
          <coneGeometry args={[ARROWHEAD_RADIUS, ARROWHEAD_LENGTH, 16]} />
          <meshStandardMaterial
            key={dimmed ? "dimmed" : "solid"}
            color={SHADING_PARAM_TUBE_COLOR}
            emissive={TUBE_EMISSIVE_COLOR}
            emissiveIntensity={SHADING_PARAM_TUBE_EMISSIVE_INTENSITY}
            transparent={dimmed}
            opacity={dimmed ? 0.25 : 1}
          />
        </mesh>
      )}
    </>
  );
}

// One edge's cyan bidirectional double-link overlay: thin tube (radius 1.0) + an
// outward-pointing arrowhead at each end. Mirrors DoubleEdgeOverlay (scene-graph.tsx).
function DoubleEdgeOverlayBuf({ seg }: { seg: EdgeSeg }) {
  const { lineGeo, arrowStart, arrowEnd } = useMemo(() => {
    const start = new THREE.Vector3(seg.sx, seg.sy, seg.sz);
    const end = new THREE.Vector3(seg.ex, seg.ey, seg.ez);
    const curve = new THREE.LineCurve3(start, end);
    const _lineGeo = new THREE.TubeGeometry(curve, 1, 1.0, 6, false);
    const dir = end.clone().sub(start);
    let _arrowStart: { center: THREE.Vector3; q: THREE.Quaternion } | null = null;
    let _arrowEnd: { center: THREE.Vector3; q: THREE.Quaternion } | null = null;
    if (dir.length() >= 1e-6) {
      const dirNorm = dir.clone().normalize();
      _arrowStart = buildArrow(start, dirNorm.clone().negate(), DL_ARROWHEAD_LENGTH);
      _arrowEnd = buildArrow(end, dirNorm, DL_ARROWHEAD_LENGTH);
    }
    return { lineGeo: _lineGeo, arrowStart: _arrowStart, arrowEnd: _arrowEnd };
  }, [seg.sx, seg.sy, seg.sz, seg.ex, seg.ey, seg.ez]);

  useEffect(() => () => { lineGeo.dispose(); }, [lineGeo]);

  const cone = (a: { center: THREE.Vector3; q: THREE.Quaternion }) => (
    <mesh
      position={[a.center.x, a.center.y, a.center.z]}
      quaternion={[a.q.x, a.q.y, a.q.z, a.q.w]}
      raycast={() => null}
    >
      <coneGeometry args={[DL_ARROWHEAD_RADIUS, DL_ARROWHEAD_LENGTH, 16]} />
      <meshStandardMaterial
        color={SHADING_PARAM_DOUBLE_LINKS_COLOR}
        emissive={DOUBLE_LINKS_EMISSIVE_COLOR}
        emissiveIntensity={SHADING_PARAM_DOUBLE_LINKS_EMISSIVE_INTENSITY}
      />
    </mesh>
  );

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
      {arrowStart && cone(arrowStart)}
      {arrowEnd && cone(arrowEnd)}
    </>
  );
}

function EdgeTubes({ capacity }: { capacity: number }) {
  const [segs, setSegs] = useState<EdgeSeg[]>([]);
  const [showDouble, setShowDouble] = useState(false);
  const keyRef = useRef<string>("");

  useFrame(() => {
    const snap = getLatestSnapshot();
    if (!snap) return;
    const decoded = decodeSnapshot(snap);
    if (!decoded) return;
    const { edgeCount, edgeView, overlayView } = decoded;

    const dbl = !!readOverlayOverlaysVis(overlayView) && !!readOverlayDoubleLinks(overlayView);
    const n = Math.min(edgeCount, capacity);
    const next: EdgeSeg[] = new Array<EdgeSeg>(n);
    let key = dbl ? "D|" : "S|";
    for (let i = 0; i < n; i++) {
      const s: EdgeSeg = {
        sx: readEdgeSX(edgeView, i), sy: readEdgeSY(edgeView, i), sz: readEdgeSZ(edgeView, i),
        ex: readEdgeEX(edgeView, i), ey: readEdgeEY(edgeView, i), ez: readEdgeEZ(edgeView, i),
      };
      next[i] = s;
      key += `${s.sx},${s.sy},${s.sz}:${s.ex},${s.ey},${s.ez};`;
    }
    // Rebuild the segment set (and thus the tube geometries) only when something moved
    // or the double-links flag flipped — not every frame.
    if (key !== keyRef.current) {
      keyRef.current = key;
      setSegs(next);
      setShowDouble(dbl);
    }
  });

  return (
    <>
      {segs.map((s, i) => (
        <React.Fragment key={i}>
          <EdgeTube seg={s} dimmed={showDouble} />
          {showDouble && <DoubleEdgeOverlayBuf seg={s} />}
        </React.Fragment>
      ))}
    </>
  );
}

// ── Missed-bead markers ─────────────────────────────────────────────────────────
// Renders the missed/ignored bead just outside a node while Go reports a firing error
// (node TorusRed=1). World position (MX/MY/MZ) and the missed value (MissVal) come from
// the buffer's Node block. Mirrors MissedBeadMarkers (scene-beads.tsx): a pooled sphere+
// ring per active node, colored by beadStyleForValue(MissVal), self-glowing with a
// cosmetic render-clock pulse. TorusRed=0 → the node's marker hides. Nodes whose MissVal
// has no bead-style are hidden (never a fallback color), matching the JSON path.
const MISSED_POOL = 16;
const MISSED_BEAD_R = 9;

function MissedBeadMarkersBuf() {
  const slotRefs = useRef<(THREE.Group | null)[]>([]);
  const sphereMatRefs = useRef<(THREE.MeshStandardMaterial | null)[]>([]);
  const torusMatRefs = useRef<(THREE.MeshStandardMaterial | null)[]>([]);

  useFrame((state) => {
    const slots = slotRefs.current;
    const snap = getLatestSnapshot();
    const decoded = snap ? decodeSnapshot(snap) : null;
    // Cosmetic pulse (0..1) from the render clock, shared by all active markers.
    const pulse = 0.5 + 0.5 * Math.sin(state.clock.elapsedTime * 8);
    let slot = 0;
    if (decoded) {
      const { nodeCount, nodeView } = decoded;
      for (let i = 0; i < nodeCount && slot < MISSED_POOL; i++) {
        if (!readNodeTorusRed(nodeView, i)) continue;
        const g = slots[slot];
        if (!g) { slot++; continue; }
        const style = beadStyleForValue(readNodeMissVal(nodeView, i));
        if (!style) { g.visible = false; slot++; continue; }
        g.position.set(readNodeMX(nodeView, i), readNodeMY(nodeView, i), readNodeMZ(nodeView, i));
        g.scale.setScalar(1.0 + 0.25 * pulse);
        const sm = sphereMatRefs.current[slot];
        if (sm) {
          sm.color.set(style.fill);
          sm.emissive.set(style.fill);
          sm.emissiveIntensity = 0.5 + 1.2 * pulse;
        }
        torusMatRefs.current[slot]?.color.set(style.ring);
        g.visible = true;
        slot++;
      }
    }
    for (let i = slot; i < MISSED_POOL; i++) {
      const g = slots[i];
      if (g) g.visible = false;
    }
  });

  return (
    <>
      {Array.from({ length: MISSED_POOL }, (_, i) => (
        <group key={i} ref={(el) => { slotRefs.current[i] = el; }} visible={false}>
          <mesh raycast={() => null}>
            <sphereGeometry args={[MISSED_BEAD_R, 16, 16]} />
            <meshStandardMaterial ref={(el) => { sphereMatRefs.current[i] = el; }} emissiveIntensity={0} />
          </mesh>
          <mesh raycast={() => null}>
            <torusGeometry args={[MISSED_BEAD_R, MISSED_BEAD_R * 0.12, 8, 24]} />
            <meshStandardMaterial ref={(el) => { torusMatRefs.current[i] = el; }} emissiveIntensity={0} />
          </mesh>
        </group>
      ))}
    </>
  );
}

// ── Node status-red pulse rings ───────────────────────────────────────────────────
// Per-node RED alarm ring drawn OVER every node whose buffer TorusRed=1. The shared-
// material NodeInstances ring can't do a per-instance emissive/scale pulse, so this draws
// a SEPARATE pulsing red ring per active node (a pooled group per node, like
// SelectionHighlight). Mirrors the pre-branch GraphNode useFrame status pulse EXACTLY:
// color+emissive #ff1a1a, emissiveIntensity 0.6 + 1.9*pulse, scale 1.18 + 0.14*pulse,
// pulse = 0.5 + 0.5*sin(elapsedTime*8) (~8Hz, cosmetic render-clock only — decides NO
// model state). The ring geometry is the unit border torus (major radius 1, tube ratio
// NODE_RING_TUBE_RATIO) scaled by the node radius × pulse scale, matching NodeInstances'
// ring. TorusRed=0 nodes get no red ring (their pool slot hides).
const NODE_STATUS_RED = "#ff1a1a";
const STATUS_RED_POOL = 16;

function NodeStatusRedRings() {
  const slotRefs = useRef<(THREE.Group | null)[]>([]);
  const matRefs = useRef<(THREE.MeshStandardMaterial | null)[]>([]);

  useFrame((state) => {
    const slots = slotRefs.current;
    const snap = getLatestSnapshot();
    const decoded = snap ? decodeSnapshot(snap) : null;
    // Cosmetic 0..1 pulse from the render clock (~8Hz), shared by all active rings.
    const pulse = 0.5 + 0.5 * Math.sin(state.clock.elapsedTime * 8);
    let slot = 0;
    if (decoded) {
      const { nodeCount, nodeView } = decoded;
      for (let i = 0; i < nodeCount && slot < STATUS_RED_POOL; i++) {
        if (!readNodeTorusRed(nodeView, i)) continue;
        const g = slots[slot];
        if (!g) { slot++; continue; }
        const r = readNodeRadius(nodeView, i) || NODE_SPHERE_RADIUS;
        g.position.set(readNodeCX(nodeView, i), readNodeCY(nodeView, i), readNodeCZ(nodeView, i));
        // Scale: node radius × the pre-branch pulse scale (1.18 + 0.14*pulse). The child
        // torus is built at major radius 1, so this lands the red ring on the node border.
        g.scale.setScalar(r * (1.18 + 0.14 * pulse));
        const mat = matRefs.current[slot];
        if (mat) mat.emissiveIntensity = 0.6 + 1.9 * pulse;
        g.visible = true;
        slot++;
      }
    }
    for (let i = slot; i < STATUS_RED_POOL; i++) {
      const g = slots[i];
      if (g) g.visible = false;
    }
  });

  return (
    <>
      {Array.from({ length: STATUS_RED_POOL }, (_, i) => (
        <group key={i} ref={(el) => { slotRefs.current[i] = el; }} visible={false}>
          <mesh raycast={() => null}>
            <torusGeometry args={[1, NODE_RING_TUBE_RATIO, 8, 32]} />
            <meshStandardMaterial
              ref={(el) => { matRefs.current[i] = el; }}
              color={NODE_STATUS_RED}
              emissive={NODE_STATUS_RED}
              emissiveIntensity={0.6}
            />
          </mesh>
        </group>
      ))}
    </>
  );
}

// ── BufferCamera ───────────────────────────────────────────────────────────────
// Buffer-driven camera: each frame reads the snapshot's single Camera row and drives
// the three.js camera (position / up / lookAt) from it. This REPLACES the old
// JSON-trace camera path (CameraFromStore ← useCameraStore ← pump) under the new-system
// flag — Go still owns the camera, but the render side now reads it from the binary
// buffer instead of the Zustand camera-store.
//
// The polar→cartesian mapping is IDENTICAL to CameraFromStore's (anglesToWorldOffset in
// viewpoint-bridge), so a given Go camera state produces the same three.js pose on
// either path:
//   pivot   = (PX, PY, PZ)
//   position = pivot + anglesToWorldOffset(R, PosTheta, PosPhi)
//   up      = anglesToWorldOffset(1, UpTheta, UpPhi).normalize()
//   lookAt(pivot)
//
// Also keeps `cameraRef` current (the old CameraRefBridge did this; it is gated off
// under the flag, but raw-input / HomeButton still read cameraRef.current).
function BufferCamera({ cameraRef }: {
  cameraRef?: React.MutableRefObject<THREE.PerspectiveCamera | null>;
}) {
  const { camera } = useThree();
  const pivotRef = useRef(new THREE.Vector3());

  useFrame(() => {
    const cam = camera as THREE.PerspectiveCamera;
    if (cameraRef) cameraRef.current = cam; // keep the ref alive for raw-input / HomeButton

    const snap = getLatestSnapshot();
    if (!snap) return;
    const decoded = decodeSnapshot(snap);
    if (!decoded) return;
    const cv = decoded.cameraView;

    const r = readCameraR(cv);
    // Guard the uninitialized camera row: Go emits a real viewpoint on load (SeedInitialViewpoint
    // reads the saved pose from view/scene.json, or a non-degenerate default), but node-geometry
    // snapshots can land first, with the camera row still all zeros. r <= 0 means "no viewpoint
    // yet" — skip, mirroring CameraFromStore's `!polar`.
    if (!(r > 0)) return;

    const pivot = pivotRef.current;
    pivot.set(readCameraPX(cv), readCameraPY(cv), readCameraPZ(cv));
    const posOffset = anglesToWorldOffset(r, readCameraPosTheta(cv), readCameraPosPhi(cv));
    cam.position.copy(pivot).add(posOffset);
    const upDir = anglesToWorldOffset(1, readCameraUpTheta(cv), readCameraUpPhi(cv)).normalize();
    cam.up.copy(upDir);
    cam.lookAt(pivot);
    cam.updateMatrixWorld(true);
  });

  return null;
}

// ── BufferLabelProjector ────────────────────────────────────────────────────────
// Buffer-driven node-label projector: each ~2 frames it reads the snapshot's node
// block, projects each node's top (center.y+radius) and center to screen, and reports
// {id,px,py,cx,cy} keyed by the buffer-nav id table (row i → ids[i]). Mirrors the old
// JSON-path LabelProjector but sourced from the buffer + id table instead of the
// RFNode array. The DOM pills/badges (ThreeView) render from these positions under the
// new-system flag. Pure projection — no geometry math, no store writes.
const _bufTopScratch = new THREE.Vector3();
const _bufCenterScratch = new THREE.Vector3();

export function BufferLabelProjector({ onPositions }: {
  onPositions: (positions: BufferLabelPos[]) => void;
}) {
  const { camera, size } = useThree();
  const frameCountRef = useRef(0);

  useFrame(() => {
    frameCountRef.current++;
    if (frameCountRef.current % 2 !== 0) return; // ~30fps, matches LabelProjector
    const snap = getLatestSnapshot();
    if (!snap) return;
    const decoded = decodeSnapshot(snap);
    if (!decoded) return;
    const { nodeCount, nodeView } = decoded;
    const ids = getNavNodeIds();
    const positions: BufferLabelPos[] = [];
    for (let i = 0; i < nodeCount; i++) {
      const cx = readNodeCX(nodeView, i);
      const cy = readNodeCY(nodeView, i);
      const cz = readNodeCZ(nodeView, i);
      const r = readNodeRadius(nodeView, i);
      _bufTopScratch.set(cx, cy + r, cz).project(camera);
      const topPx = ndcToPixel(_bufTopScratch.x, _bufTopScratch.y, size);
      _bufCenterScratch.set(cx, cy, cz).project(camera);
      const centerPx = ndcToPixel(_bufCenterScratch.x, _bufCenterScratch.y, size);
      positions.push({ id: ids[i] ?? `#${i}`, px: topPx.px, py: topPx.py, cx: centerPx.px, cy: centerPx.py });
    }
    onPositions(positions);
  });

  return null;
}

// ── BufferScene ───────────────────────────────────────────────────────────────
// Capacity manager: checks the latest snapshot each frame and grows per-block
// capacities when counts exceed current allocation, triggering a React re-render
// (which remounts the InstancedMesh/LineSegments with a larger buffer).

export function BufferScene({ cameraRef }: {
  cameraRef?: React.MutableRefObject<THREE.PerspectiveCamera | null>;
} = {}) {
  const [beadCap,  setBeadCap]  = useState(INITIAL_BEAD_CAP);
  const [nodeCap,  setNodeCap]  = useState(INITIAL_NODE_CAP);
  const [edgeCap,  setEdgeCap]  = useState(INITIAL_EDGE_CAP);
  const [portCap,  setPortCap]  = useState(INITIAL_PORT_CAP);

  // Capacity-growth guard: runs every frame to detect need for reallocation.
  useFrame(() => {
    const snap = getLatestSnapshot();
    if (!snap) return;
    const decoded = decodeSnapshot(snap);
    if (!decoded) return;

    if (decoded.beadCount > beadCap) {
      setBeadCap(Math.ceil(decoded.beadCount * 1.5));
    }
    if (decoded.nodeCount > nodeCap) {
      setNodeCap(Math.ceil(decoded.nodeCount * 1.5));
    }
    if (decoded.edgeCount > edgeCap) {
      setEdgeCap(Math.ceil(decoded.edgeCount * 1.5));
    }
    if (decoded.portCount > portCap) {
      setPortCap(Math.ceil(decoded.portCount * 1.5));
    }
  });

  return (
    <>
      <BufferCamera cameraRef={cameraRef} />
      <BeadInstances capacity={beadCap} />
      <NodeInstances capacity={nodeCap} />
      <PortInstances capacity={portCap} />
      <InteriorBeadInstances capacity={nodeCap * INTERIOR_SLOTS_PER_NODE} />
      <SelectionHighlight capacity={nodeCap} />
      <SphereRings />
      <EdgeTubes     capacity={edgeCap} />
      <MissedBeadMarkersBuf />
      <NodeStatusRedRings />
    </>
  );
}
