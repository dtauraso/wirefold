// buffer-scene.tsx — buffer-driven render path orchestrator.
//
// Reads the latest binary snapshot each frame and renders:
//   - Beads: InstancedMesh updated from bead column (positions, live flag).
//   - Nodes: InstancedMesh updated from node column (center positions).
//   - Edges: LineSegments updated from edge column (start/end endpoints).
//
// This component does NOT write to any Zustand store. It reads the snapshot
// buffer directly (zero-copy DataView slices via buffer-decode.ts) and fills
// GPU attribute arrays imperatively via useFrame. No domain state flows out.
//
// The actual per-block renderers live in sibling files (BeadInstances, NodeInstances,
// PortInstances, SelectionHighlight/HoverHighlight, SphereRings, InteriorBeadInstances,
// EdgeTube, BufferCamera, BufferLabelProjector) — this file is just the capacity-manager
// orchestrator that mounts them, plus the shared pick-tag re-exports scene-content.tsx and
// ThreeView.tsx still import from here.

import { useState } from "react";
import { useFrame } from "@react-three/fiber";
import type * as THREE from "three";
import { getLatestSnapshot } from "../snapshot-buffer";
import { decodeSnapshot } from "./buffer-decode";
import { INTERIOR_SLOTS_PER_NODE } from "./buffer-decode";
import { BeadInstances } from "./BeadInstances";
import { NodeInstances } from "./NodeInstances";
import { PortInstances } from "./PortInstances";
import { SelectionHighlight, HoverHighlight } from "./SelectionHighlight";
import { SphereRings } from "./SphereRings";
import { InteriorBeadInstances } from "./InteriorBeadInstances";
import { EdgeTubes } from "./EdgeTube";
import { BufferCamera } from "./BufferCamera";
import { BufferLabelProjector } from "./BufferLabelProjector";

export type { BufferLabelPos } from "./buffer-scene-shared";
export {
  BUFFER_NODE_TAG,
  BUFFER_PORT_TAG,
  BUFFER_RING_TAG,
  BUFFER_EDGE_TAG,
} from "./buffer-scene-shared";
export { BufferLabelProjector };

// ── Sizing constants ──────────────────────────────────────────────────────────
const INITIAL_BEAD_CAP  = 64;
const INITIAL_NODE_CAP  = 32;
const INITIAL_EDGE_CAP  = 32; // edge positions buffer: N edges × 2 endpoints × 3 floats
const INITIAL_PORT_CAP  = 64; // port spheres: one per node port (input + output), grows as needed

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
