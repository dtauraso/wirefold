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

import React, { useRef, useState } from "react";
import { useFrame, useThree } from "@react-three/fiber";
import * as THREE from "three";
import { getLatestSnapshot } from "../snapshot-buffer";
import { USE_NEW_SYSTEM } from "../new-system";
import { decodeSnapshot } from "./buffer-decode";
import { getNavNodeIds } from "./buffer-nav";
import { ndcToPixel } from "./geometry-helpers";
import { anglesToWorldOffset } from "./viewpoint-bridge";
import {
  readBeadX, readBeadY, readBeadZ, readBeadLive,
  readNodeCX, readNodeCY, readNodeCZ, readNodeRadius, readNodeSelected,
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
const NODE_SPHERE_RADIUS = 12;

// ── Sub-components (split so capacity growth triggers localised re-render) ────

function BeadInstances({ capacity }: { capacity: number }) {
  const meshRef = useRef<THREE.InstancedMesh>(null);
  const matRef  = useRef(new THREE.Matrix4());

  useFrame(() => {
    const mesh = meshRef.current;
    if (!mesh) return;

    const snap = getLatestSnapshot();
    if (!snap) { mesh.count = 0; return; }
    const decoded = decodeSnapshot(snap);
    if (!decoded) { mesh.count = 0; return; }
    const { beadCount, beadView } = decoded;

    let slot = 0;
    for (let i = 0; i < beadCount && slot < capacity; i++) {
      if (!readBeadLive(beadView, i)) continue;
      matRef.current.setPosition(
        readBeadX(beadView, i),
        readBeadY(beadView, i),
        readBeadZ(beadView, i),
      );
      mesh.setMatrixAt(slot, matRef.current);
      slot++;
    }
    mesh.count = slot;
    mesh.instanceMatrix.needsUpdate = true;
  });

  return (
    <instancedMesh ref={meshRef} args={[undefined, undefined, capacity]}>
      <sphereGeometry args={[BEAD_SPHERE_RADIUS, 8, 8]} />
      <meshBasicMaterial color="#ff8844" />
    </instancedMesh>
  );
}

function NodeInstances({ capacity }: { capacity: number }) {
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

    const n = Math.min(nodeCount, capacity);
    for (let i = 0; i < n; i++) {
      matRef.current.setPosition(
        readNodeCX(nodeView, i),
        readNodeCY(nodeView, i),
        readNodeCZ(nodeView, i),
      );
      mesh.setMatrixAt(i, matRef.current);
    }
    mesh.count = n;
    mesh.instanceMatrix.needsUpdate = true;
  });

  return (
    <instancedMesh ref={meshRef} args={[undefined, undefined, capacity]}>
      <sphereGeometry args={[NODE_SPHERE_RADIUS, 12, 12]} />
      <meshBasicMaterial color="#4488ff" wireframe />
    </instancedMesh>
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
    // Guard the uninitialized camera row: Go emits a real viewpoint (restore / home fit)
    // on load, but node-geometry snapshots can land first, with the camera row still all
    // zeros. r <= 0 means "no viewpoint yet" — skip, mirroring CameraFromStore's `!polar`.
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
      <SelectionHighlight capacity={nodeCap} />
      <EdgeLines     capacity={edgeCap} />
    </>
  );
}
