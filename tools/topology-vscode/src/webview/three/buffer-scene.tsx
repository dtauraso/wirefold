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

import React, { useRef, useState, useContext } from "react";
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
} from "../../schema/shading-params";
import {
  readBeadX, readBeadY, readBeadZ, readBeadLive, readBeadValue,
  readNodeCX, readNodeCY, readNodeCZ, readNodeRadius, readNodeSelected,
  readInteriorPresent, readInteriorValue, readInteriorOX, readInteriorOY, readInteriorOZ,
  readEdgeSX, readEdgeSY, readEdgeSZ, readEdgeEX, readEdgeEY, readEdgeEZ,
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
  });

  return (
    <>
      <instancedMesh ref={bodyRef} args={[undefined, undefined, capacity]}>
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

// Highlight ring drawn around the Go-selected node (Selected column = 1). Go OWNS the
// selection; this is pure render of the buffer's Selected column — no TS selection store.
function SelectionHighlight({ capacity }: { capacity: number }) {
  const meshRef = useRef<THREE.InstancedMesh>(null);
  const matRef  = useRef(new THREE.Matrix4());

  useFrame(() => {
    const mesh = meshRef.current;
    if (!mesh) return;

    const snap = getLatestSnapshot();
    if (!snap) { mesh.count = 0; return; }
    const decoded = decodeSnapshot(snap);
    if (!decoded) { mesh.count = 0; return; }
    const { nodeCount, nodeView } = decoded;

    let slot = 0;
    for (let i = 0; i < nodeCount && slot < capacity; i++) {
      if (!readNodeSelected(nodeView, i)) continue;
      matRef.current.setPosition(
        readNodeCX(nodeView, i),
        readNodeCY(nodeView, i),
        readNodeCZ(nodeView, i),
      );
      mesh.setMatrixAt(slot, matRef.current);
      slot++;
    }
    mesh.count = slot;
    mesh.instanceMatrix.needsUpdate = true;
  });

  return (
    <instancedMesh ref={meshRef} args={[undefined, undefined, capacity]}>
      <sphereGeometry args={[NODE_SPHERE_RADIUS * 1.25, 16, 16]} />
      <meshBasicMaterial color="#ffee44" wireframe transparent opacity={0.85} />
    </instancedMesh>
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

function EdgeLines({ capacity }: { capacity: number }) {
  const linesRef  = useRef<THREE.LineSegments>(null);
  const geoRef    = useRef(new THREE.BufferGeometry());
  const posRef    = useRef(new Float32Array(capacity * 6)); // capacity edges × 2 pts × 3

  // Initialize geometry attribute on first render.
  React.useLayoutEffect(() => {
    const attr = new THREE.BufferAttribute(posRef.current, 3);
    attr.setUsage(THREE.DynamicDrawUsage);
    geoRef.current.setAttribute("position", attr);
  }, []);

  useFrame(() => {
    const snap = getLatestSnapshot();
    if (!snap) return;
    const decoded = decodeSnapshot(snap);
    if (!decoded) return;
    const { edgeCount, edgeView } = decoded;

    const pos = posRef.current;
    const n = Math.min(edgeCount, capacity);
    for (let i = 0; i < n; i++) {
      const b = i * 6;
      pos[b]     = readEdgeSX(edgeView, i);
      pos[b + 1] = readEdgeSY(edgeView, i);
      pos[b + 2] = readEdgeSZ(edgeView, i);
      pos[b + 3] = readEdgeEX(edgeView, i);
      pos[b + 4] = readEdgeEY(edgeView, i);
      pos[b + 5] = readEdgeEZ(edgeView, i);
    }

    const geo = geoRef.current;
    geo.setDrawRange(0, n * 2); // 2 vertices per segment
    const attr = geo.attributes.position as THREE.BufferAttribute;
    attr.needsUpdate = true;

    const lines = linesRef.current;
    if (lines) lines.visible = n > 0;
  });

  return (
    <lineSegments ref={linesRef} geometry={geoRef.current}>
      <lineBasicMaterial color="#44ff88" />
    </lineSegments>
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
  });

  return (
    <>
      <BufferCamera cameraRef={cameraRef} />
      <BeadInstances capacity={beadCap} />
      <NodeInstances capacity={nodeCap} />
      <InteriorBeadInstances capacity={nodeCap * INTERIOR_SLOTS_PER_NODE} />
      <SelectionHighlight capacity={nodeCap} />
      <EdgeLines     capacity={edgeCap} />
    </>
  );
}
