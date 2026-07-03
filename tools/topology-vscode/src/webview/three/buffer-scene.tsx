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
import { decodeSnapshot, nodeLabel } from "./buffer-decode";
import { NODE_DEFS_ARRAY } from "../../schema/node-defs";
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
  SHADING_PARAM_NODE_FADE_OPACITY,
  SHADING_PARAM_NODE_FADE_BODY_MUL,
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
  readNodeSphereR, readNodeVRX, readNodeVRY, readNodeVRZ, readNodeFRX, readNodeFRY, readNodeFRZ,
  readNodeKindId,
  readInteriorPresent, readInteriorValue, readInteriorOX, readInteriorOY, readInteriorOZ,
  readEdgeSX, readEdgeSY, readEdgeSZ, readEdgeEX, readEdgeEY, readEdgeEZ,
  readEdgeSrcNodeRow, readEdgeDstNodeRow, readEdgeSelected, readEdgeFaded,
  readNodeFaded, readNodeHovered,
  readPortNodeRow, readPortPX, readPortPY, readPortPZ, readPortHovered,
  readOverlayOverlaysVis, readOverlayDoubleLinks, readOverlaySelMode,
  readCameraPX, readCameraPY, readCameraPZ, readCameraR,
  readCameraPosTheta, readCameraPosPhi, readCameraUpTheta, readCameraUpPhi,
} from "../../schema/buffer-layout";

/** Projected label position for one buffer node row. `row` is the node's buffer node-row
 *  index (identity); `label` is its human label decoded from the buffer's label section. */
export interface BufferLabelPos { row: number; label: string; px: number; py: number; cx: number; cy: number; }

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
// userData tag marking the NodeInstances border-ring InstancedMesh as the pickable TORUS
// target (a `port ∈ torus` lock is captured by picking a port then this ring). Instance i
// IS the node row (same loop that draws the body mesh), so a hit's instanceId resolves to
// the owning node id exactly like BUFFER_NODE_TAG.
export const BUFFER_RING_TAG = "bufferRing";
// userData key marking a per-edge wide pick-halo mesh (buffer-scene.tsx EdgeTube) as the pickable
// EDGE target under the new system. Unlike the node/port tags (a boolean, resolved via the
// InstancedMesh instanceId), edges are individual meshes, so this key HOLDS the numeric buffer
// EDGE-ROW index directly. On a hit, RaycasterHelper (scene-content.tsx pickBufferEdge) reads
// userData[BUFFER_EDGE_TAG] as the edge row and forwards it to Go, which resolves the row back to
// its edge. No edge-label string is rendered or sent (mirrors the port-row scheme).
export const BUFFER_EDGE_TAG = "bufferEdgeRow";
// Port hit-sphere radius (world units): the small grabbable ball drawn at each port. Matches
// the pre-branch PortSphere (scene-graph.tsx PORT_SPHERE_R).
const PORT_SPHERE_R = 4;
// Border-ring tube thickness as a fraction of the node radius (mirrors GraphNode's
// resting torusThick = r * 0.08).
const NODE_RING_TUBE_RATIO = 0.08;
// Pointer-hover highlight (pre-branch scene-graph.tsx): the hovered node's border ring turns
// #aaddff and thickens to r*0.14 (HOVER_RING_TUBE_RATIO); a hovered port sphere turns #aaddff
// and grows to 1.3× (PortSphere isHov). Go OWNS hover (the Hovered columns); this is render-only.
const HOVER_COLOR = "#aaddff";
const HOVER_RING_TUBE_RATIO = 0.14;
const PORT_HOVER_COLOR = HOVER_COLOR;
const PORT_HOVER_SCALE = 1.3;

/**
 * Resolve a node row's fill/stroke from its KindId column in the buffer.
 * Reads KindId (u8) at the given row and indexes NODE_DEFS_ARRAY; falls back to
 * grey when the id is out-of-range (0xFF sentinel = unknown kind).
 */
export function nodeRowColors(nodeView: DataView, row: number): { fill: string; stroke: string } {
  const kindId = readNodeKindId(nodeView, row);
  const def = NODE_DEFS_ARRAY[kindId];
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
      <instancedMesh ref={bodyRef} args={[undefined, undefined, capacity]} frustumCulled={false}>
        <sphereGeometry args={[BEAD_SPHERE_RADIUS, 16, 16]} />
        <meshStandardMaterial emissiveIntensity={0} />
      </instancedMesh>
      <instancedMesh ref={ringRef} args={[undefined, undefined, capacity]} frustumCulled={false}>
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
// Per-instance FADE: an `aFaded` instanced attribute (0|1) drives a per-node alpha multiply
// injected into the shared material (onBeforeCompile). A single InstancedMesh keeps
// instanceId == node row (so a faded node is still pickable to un-fade it) while faded rows
// render dimmed. The multipliers are the pre-branch faded/solid opacity RATIOS: body target
// = FADE_OPACITY*BODY_MUL vs solid NODE_OPACITY; ring target = FADE_OPACITY vs solid 1.
const NODE_BODY_FADE_MUL = (SHADING_PARAM_NODE_FADE_OPACITY * SHADING_PARAM_NODE_FADE_BODY_MUL) / SHADING_PARAM_NODE_OPACITY;
const NODE_RING_FADE_MUL = SHADING_PARAM_NODE_FADE_OPACITY;
const glslFloat = (n: number): string => (Number.isInteger(n) ? n.toFixed(1) : String(n));
function makeFadeAlphaPatch(mul: number) {
  return (shader: { vertexShader: string; fragmentShader: string }) => {
    shader.vertexShader = "attribute float aFaded;\nvarying float vFaded;\n" +
      shader.vertexShader.replace("void main() {", "void main() {\n  vFaded = aFaded;");
    shader.fragmentShader = "varying float vFaded;\n" +
      shader.fragmentShader.replace(
        "#include <dithering_fragment>",
        `  if ( vFaded > 0.5 ) gl_FragColor.a *= ${glslFloat(mul)};\n#include <dithering_fragment>`,
      );
  };
}
const patchBodyFade = makeFadeAlphaPatch(NODE_BODY_FADE_MUL);
const patchRingFade = makeFadeAlphaPatch(NODE_RING_FADE_MUL);

function NodeInstances({ capacity }: { capacity: number }) {
  const envTex = useContext(EnvTexContext);
  const bodyRef = useRef<THREE.InstancedMesh>(null);
  const ringRef = useRef<THREE.InstancedMesh>(null);
  const bodyFadedRef = useRef<THREE.InstancedBufferAttribute>(null);
  const ringFadedRef = useRef<THREE.InstancedBufferAttribute>(null);
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

      const { fill, stroke } = nodeRowColors(nodeView, i);
      body.setColorAt(i, colRef.current.set(fill));
      ring.setColorAt(i, colRef.current.set(stroke));

      // Per-instance fade flag → aFaded attribute (drives the shader alpha multiply).
      const faded = readNodeFaded(nodeView, i) ? 1 : 0;
      const bf = bodyFadedRef.current;
      const rf = ringFadedRef.current;
      if (bf) (bf.array as Float32Array)[i] = faded;
      if (rf) (rf.array as Float32Array)[i] = faded;
    }
    body.count = n;
    ring.count = n;
    body.instanceMatrix.needsUpdate = true;
    ring.instanceMatrix.needsUpdate = true;
    if (body.instanceColor) body.instanceColor.needsUpdate = true;
    if (ring.instanceColor) ring.instanceColor.needsUpdate = true;
    if (bodyFadedRef.current) bodyFadedRef.current.needsUpdate = true;
    if (ringFadedRef.current) ringFadedRef.current.needsUpdate = true;
    // Refresh the InstancedMesh bounding sphere so raycast picking stays accurate as
    // nodes move (three.js early-outs a ray against a cached union sphere; a dragged
    // node outside the stale sphere would otherwise be un-pickable). Cheap for the
    // small node counts here.
    body.computeBoundingSphere();
  });

  return (
    <>
      <instancedMesh ref={bodyRef} args={[undefined, undefined, capacity]} userData={{ [BUFFER_NODE_TAG]: true }} frustumCulled={false}>
        <sphereGeometry args={[1, 16, 16]}>
          <instancedBufferAttribute ref={bodyFadedRef} attach="attributes-aFaded" args={[new Float32Array(capacity), 1]} />
        </sphereGeometry>
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
          onBeforeCompile={patchBodyFade}
        />
      </instancedMesh>
      <instancedMesh ref={ringRef} args={[undefined, undefined, capacity]} userData={{ [BUFFER_RING_TAG]: true }} frustumCulled={false}>
        <torusGeometry args={[1, NODE_RING_TUBE_RATIO, 8, 32]}>
          <instancedBufferAttribute ref={ringFadedRef} attach="attributes-aFaded" args={[new Float32Array(capacity), 1]} />
        </torusGeometry>
        {/* transparent so faded rings (aFaded=1) blend at reduced alpha; solid rings keep
            alpha 1 → visually opaque. */}
        <meshStandardMaterial roughness={0.6} metalness={0} transparent depthWrite={false} onBeforeCompile={patchRingFade} />
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
        // Pointer hover (Go-owned Hovered column): pre-branch PortSphere isHov look — the
        // port sphere grows (scale 1.3) and turns #aaddff. Unhovered stays scale 1 + owner
        // stroke. (No port-selected concept in the buffer path, so selected-1.5 is n/a.)
        const hov = readPortHovered(portView, i) !== 0;
        sclRef.current.setScalar(hov ? PORT_HOVER_SCALE : 1);
        // World placement = Go's streamed authoritative port world position (PX/PY/PZ) —
        // the SAME point the connected edge's endpoint uses (portWorldPosAimed), so the
        // marker IS the edge endpoint by construction (no client-side recompute).
        posRef.current.set(
          readPortPX(portView, i),
          readPortPY(portView, i),
          readPortPZ(portView, i),
        );
        mesh.setColorAt(i, colRef.current.set(hov ? PORT_HOVER_COLOR : nodeRowColors(nodeView, nodeRow).stroke));
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
    <instancedMesh ref={meshRef} args={[undefined, undefined, capacity]} userData={{ [BUFFER_PORT_TAG]: true }} frustumCulled={false}>
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
          <mesh raycast={() => null} frustumCulled={false}>
            <torusGeometry args={[1, 0.14, 8, 32]} />
            <meshStandardMaterial color="#ffcc00" emissive="#ffcc00" emissiveIntensity={0.3} />
          </mesh>
          {/* Orange halo sphere — matches GraphNode NODE_HALO_R_RATIO=1.45 */}
          <mesh raycast={() => null} frustumCulled={false}>
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

// Hover highlight drawn around the Go-hovered node (the Hovered column). Mirrors the
// pre-branch GraphNode hover look: a #aaddff border ring thickened to r*0.14 (same thickness
// the selection/on-surface rings use). Go OWNS hover; this reads the Hovered column and draws
// a single ring — no TS hover state. SELECTION TAKES PRECEDENCE: when the hovered node is the
// selected node OR one of its on-surface highlighted nodes (which SelectionHighlight already
// rings in yellow), the hover ring is suppressed so the selection color wins. Only one node
// can be hovered at a time, so a single mesh (no pool) suffices.
function HoverHighlight() {
  const ringRef = useRef<THREE.Mesh>(null);
  const edgesRef = useRef<EdgeAdj[]>([]);

  useFrame(() => {
    const ring = ringRef.current;
    if (!ring) return;

    const snap = getLatestSnapshot();
    const decoded = snap ? decodeSnapshot(snap) : null;
    let show = false;
    if (decoded) {
      const { nodeCount, nodeView, edgeCount, edgeView, overlayView } = decoded;

      // Hovered node row (at most one).
      let hoveredRow = -1;
      for (let i = 0; i < nodeCount; i++) {
        if (readNodeHovered(nodeView, i)) { hoveredRow = i; break; }
      }
      // Selected row (at most one) — selection styling wins over hover.
      let selectedRow = -1;
      for (let i = 0; i < nodeCount; i++) {
        if (readNodeSelected(nodeView, i)) { selectedRow = i; break; }
      }

      if (hoveredRow >= 0) {
        // Suppress if the hovered node is already highlighted by the selection (selected
        // node or one of its on-surface nodes — the exact set SelectionHighlight rings).
        let suppressed = false;
        if (selectedRow >= 0) {
          const edges = edgesRef.current;
          edges.length = 0;
          for (let e = 0; e < edgeCount; e++) {
            edges.push({ src: readEdgeSrcNodeRow(edgeView, e), dst: readEdgeDstNodeRow(edgeView, e) });
          }
          const mode: SelMode = readOverlaySelMode(overlayView) ? "own" : "surface";
          suppressed = surfaceRowSet(selectedRow, mode, edges).has(hoveredRow);
        }
        if (!suppressed) {
          const r = readNodeRadius(nodeView, hoveredRow) || NODE_SPHERE_RADIUS;
          ring.position.set(
            readNodeCX(nodeView, hoveredRow),
            readNodeCY(nodeView, hoveredRow),
            readNodeCZ(nodeView, hoveredRow),
          );
          ring.scale.setScalar(r); // child torus built at major radius 1 → scales to r
          show = true;
        }
      }
    }
    ring.visible = show;
  });

  return (
    <mesh ref={ringRef} visible={false} raycast={() => null} frustumCulled={false}>
      {/* #aaddff torus ring — pre-branch hover border: r*0.14 thick */}
      <torusGeometry args={[1, HOVER_RING_TUBE_RATIO, 8, 32]} />
      <meshStandardMaterial color={HOVER_COLOR} emissive={HOVER_COLOR} emissiveIntensity={0.3} />
    </mesh>
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
      <mesh geometry={geo} quaternion={[vrQ.x, vrQ.y, vrQ.z, vrQ.w]} raycast={() => null} frustumCulled={false}>
        <meshStandardMaterial
          color={ring.color}
          emissive={ring.color}
          emissiveIntensity={SPHERE_RING_EMISSIVE_INTENSITY}
          transparent
          opacity={SPHERE_RING_OPACITY}
          depthWrite={false}
        />
      </mesh>
      <mesh geometry={geo} quaternion={[frQ.x, frQ.y, frQ.z, frQ.w]} raycast={() => null} frustumCulled={false}>
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
            color: nodeRowColors(nodeView, row).stroke,
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
      <instancedMesh ref={bodyRef} args={[undefined, undefined, capacity]} frustumCulled={false}>
        <sphereGeometry args={[1, 16, 16]} />
        <meshStandardMaterial />
      </instancedMesh>
      <instancedMesh ref={ringRef} args={[undefined, undefined, capacity]} frustumCulled={false}>
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
// Edge selection/pick halo radius (world units) — the pre-branch SingleEdgeTube halo
// (TubeGeometry(curve,1,5,6)). This wide concentric tube is ALWAYS mounted per edge as the
// raycast pick target (opacity 0 when unselected but still hittable) and painted orange
// (#ff5a00, opacity 0.6) on the Go-selected edge.
const EDGE_HALO_RADIUS = 5;
const EDGE_HALO_COLOR = "#ff5a00";
const EDGE_HALO_SELECTED_OPACITY = 0.6;
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
// `dimmed` (double-links on) drops opacity to 0.25 like the JSON path. `row` is this edge's
// buffer EDGE-ROW index — stamped on the wide halo's userData[BUFFER_EDGE_TAG] as the pickable
// edge target (mirrors the pre-branch SingleEdgeTube halo, which doubled as the pick tube).
// `selected` paints that halo orange (opacity 0.6) when Go marks this edge selected; otherwise
// the halo stays opacity 0 but remains raycast-hittable.
function EdgeTube({ seg, dimmed, row, selected, faded }: { seg: EdgeSeg; dimmed: boolean; row: number; selected: boolean; faded: boolean }) {
  // Faded edge: dim the tube (mirror pre-branch SingleEdgeTube `faded ? FADE_OPACITY : …`).
  // Fade takes precedence over the double-links dim. The traveling bead is suppressed
  // Go-side (a faded edge's bead rows stream Live=0), so no bead-hiding is needed here.
  const tubeTransparent = faded || dimmed;
  const tubeOpacity = faded ? SHADING_PARAM_NODE_FADE_OPACITY : dimmed ? 0.25 : 1;
  const matKey = faded ? "faded" : dimmed ? "dimmed" : "solid";
  const { tubeGeo, haloGeo, arrow } = useMemo(() => {
    const start = new THREE.Vector3(seg.sx, seg.sy, seg.sz);
    const end = new THREE.Vector3(seg.ex, seg.ey, seg.ez);
    const curve = new THREE.LineCurve3(start, end);
    const _tubeGeo = new THREE.TubeGeometry(curve, 1, 1.5, 6, false);
    // Wide concentric halo on the same segment — the pre-branch pick radius (5).
    const _haloGeo = new THREE.TubeGeometry(curve, 1, EDGE_HALO_RADIUS, 6, false);
    const dir = end.clone().sub(start);
    let _arrow: { center: THREE.Vector3; q: THREE.Quaternion } | null = null;
    if (dir.length() >= 1e-6) {
      dir.normalize();
      _arrow = buildArrow(end, dir, ARROWHEAD_LENGTH);
    }
    return { tubeGeo: _tubeGeo, haloGeo: _haloGeo, arrow: _arrow };
  }, [seg.sx, seg.sy, seg.sz, seg.ex, seg.ey, seg.ez]);

  // R3F does not auto-dispose an imperatively-passed geometry={...}; dispose on rebuild/unmount.
  useEffect(() => () => { tubeGeo.dispose(); haloGeo.dispose(); }, [tubeGeo, haloGeo]);

  return (
    <>
      <mesh geometry={tubeGeo} raycast={() => null} frustumCulled={false}>
        <meshStandardMaterial
          key={matKey}
          color={SHADING_PARAM_TUBE_COLOR}
          emissive={TUBE_EMISSIVE_COLOR}
          emissiveIntensity={SHADING_PARAM_TUBE_EMISSIVE_INTENSITY}
          transparent={tubeTransparent}
          opacity={tubeOpacity}
        />
      </mesh>
      {/* Selection halo doubles as the wide pick target (pre-branch SingleEdgeTube). Always
          mounted so the raycaster can hit anywhere within the halo radius; painted only when
          selected (opacity 0 otherwise — an opacity-0 mesh is still raycast-hittable). */}
      <mesh geometry={haloGeo} userData={{ [BUFFER_EDGE_TAG]: row }} frustumCulled={false}>
        <meshBasicMaterial
          color={EDGE_HALO_COLOR}
          transparent
          opacity={selected ? EDGE_HALO_SELECTED_OPACITY : 0}
          side={THREE.DoubleSide}
          depthWrite={false}
        />
      </mesh>
      {arrow && (
        <mesh
          position={[arrow.center.x, arrow.center.y, arrow.center.z]}
          quaternion={[arrow.q.x, arrow.q.y, arrow.q.z, arrow.q.w]}
          raycast={() => null}
          frustumCulled={false}
        >
          <coneGeometry args={[ARROWHEAD_RADIUS, ARROWHEAD_LENGTH, 16]} />
          <meshStandardMaterial
            key={matKey}
            color={SHADING_PARAM_TUBE_COLOR}
            emissive={TUBE_EMISSIVE_COLOR}
            emissiveIntensity={SHADING_PARAM_TUBE_EMISSIVE_INTENSITY}
            transparent={tubeTransparent}
            opacity={tubeOpacity}
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
      frustumCulled={false}
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
      <mesh geometry={lineGeo} raycast={() => null} frustumCulled={false}>
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
  // The Go-selected edge's buffer row (-1 = none). Tracked separately from the segment set
  // so a selection change (which does NOT move any endpoint) toggles the halo without
  // rebuilding the tube geometries. Go OWNS the selection (Edge block Selected column).
  const [selRow, setSelRow] = useState(-1);
  // Faded edge rows (Go-owned fade fixpoint, Edge Faded column). Tracked separately from the
  // segment set — a fade toggle does NOT move any endpoint, so it dims the tube without
  // rebuilding geometry (mirrors selRow).
  const [fadedRows, setFadedRows] = useState<boolean[]>([]);
  const fadedKeyRef = useRef<string>("");
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
    const fadedNext: boolean[] = new Array<boolean>(n);
    let key = dbl ? "D|" : "S|";
    let fkey = "";
    let sel = -1;
    for (let i = 0; i < n; i++) {
      const s: EdgeSeg = {
        sx: readEdgeSX(edgeView, i), sy: readEdgeSY(edgeView, i), sz: readEdgeSZ(edgeView, i),
        ex: readEdgeEX(edgeView, i), ey: readEdgeEY(edgeView, i), ez: readEdgeEZ(edgeView, i),
      };
      next[i] = s;
      key += `${s.sx},${s.sy},${s.sz}:${s.ex},${s.ey},${s.ez};`;
      if (sel < 0 && readEdgeSelected(edgeView, i)) sel = i;
      const f = !!readEdgeFaded(edgeView, i);
      fadedNext[i] = f;
      fkey += f ? "1" : "0";
    }
    // Rebuild the segment set (and thus the tube geometries) only when something moved
    // or the double-links flag flipped — not every frame.
    if (key !== keyRef.current) {
      keyRef.current = key;
      setSegs(next);
      setShowDouble(dbl);
    }
    // Selection toggles cheaply (no geometry rebuild) — update only when the row changes.
    if (sel !== selRow) setSelRow(sel);
    // Fade toggles cheaply too (opacity only, no geometry rebuild).
    if (fkey !== fadedKeyRef.current) {
      fadedKeyRef.current = fkey;
      setFadedRows(fadedNext);
    }
  });

  return (
    <>
      {segs.map((s, i) => (
        <React.Fragment key={i}>
          <EdgeTube seg={s} dimmed={showDouble} row={i} selected={i === selRow} faded={!!fadedRows[i]} />
          {showDouble && <DoubleEdgeOverlayBuf seg={s} />}
        </React.Fragment>
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
// Buffer-driven node label projector: each ~2 frames it reads the snapshot's node
// block, projects each node's top (center.y+radius) and center to screen, and reports
// {row,label,px,py,cx,cy} — the row is the node's buffer node-row index (identity) and the
// label is decoded straight from the buffer's label section (nodeLabel). Mirrors the old
// JSON-path LabelProjector but sourced entirely from the buffer, no id table. The DOM
// pills/badges (ThreeView) render from these positions. Pure projection — no store writes.
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
      positions.push({ row: i, label: nodeLabel(decoded, i), px: topPx.px, py: topPx.py, cx: centerPx.px, cy: centerPx.py });
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
      <HoverHighlight />
      <SphereRings />
      <EdgeTubes     capacity={edgeCap} />
    </>
  );
}
