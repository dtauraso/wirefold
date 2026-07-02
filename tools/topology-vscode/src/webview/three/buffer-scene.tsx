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
import { useFrame } from "@react-three/fiber";
import * as THREE from "three";
import { getLatestSnapshot } from "../snapshot-buffer";
import { USE_NEW_SYSTEM } from "../new-system";
import { decodeSnapshot } from "./buffer-decode";
import {
  readBeadX, readBeadY, readBeadZ, readBeadLive,
  readNodeCX, readNodeCY, readNodeCZ, readNodeSelected,
  readEdgeSX, readEdgeSY, readEdgeSZ, readEdgeEX, readEdgeEY, readEdgeEZ,
} from "../../schema/buffer-layout";

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

// ── BufferScene ───────────────────────────────────────────────────────────────
// Capacity manager: checks the latest snapshot each frame and grows per-block
// capacities when counts exceed current allocation, triggering a React re-render
// (which remounts the InstancedMesh/LineSegments with a larger buffer).

export function BufferScene() {
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
      <BeadInstances capacity={beadCap} />
      <NodeInstances capacity={nodeCap} />
      <SelectionHighlight capacity={nodeCap} />
      <EdgeLines     capacity={edgeCap} />
    </>
  );
}
