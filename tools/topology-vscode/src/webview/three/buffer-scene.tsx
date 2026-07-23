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
import { getEdgeStreamAccessor } from "./edge-stream-blocks";
import { getNodeFrame, getLayoutLinks } from "./node-stream-blocks";
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
const INITIAL_LAYOUTLINK_CAP = 32; // layout double-link overlay pairs — from LocalPolars, NOT the Edge block, so its count is independent of edgeCount and needs its OWN cap

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
  const [layoutLinkCap, setLayoutLinkCap] = useState(INITIAL_LAYOUTLINK_CAP);

  // Capacity-growth guard: runs every frame to detect need for reallocation. EVERY
  // variable-length streamed block must have a row here — a block whose count outgrows a
  // cap that isn't tracked is silently clamped (the layout-link overlay lost links this
  // way, borrowing edgeCap). Listing them in ONE table (not scattered ifs) makes a new
  // block's capacity a single obvious edit and its omission a visible gap in this list.
  useFrame(() => {
    const grow: { count: number; cap: number; set: (n: number) => void }[] = [];

    // Layout links are aggregated from the per-node dedicated streams' own outbound
    // layout-links (node-stream-blocks.ts's getLayoutLinks) — independent of edge/bead/node
    // stream arrival.
    const { layoutLinkCount } = getLayoutLinks();
    grow.push({ count: layoutLinkCount, cap: layoutLinkCap, set: setLayoutLinkCap });

    // Every edge's own dedicated stream frame reports its own geometry+beads
    // (edge-stream-blocks.ts) — grow edgeCap off the edge-row count, and beadCap off the
    // total bead count summed across every edge row.
    const edgeStream = getEdgeStreamAccessor();
    if (edgeStream) {
      grow.push({ count: edgeStream.edgeCount, cap: edgeCap, set: setEdgeCap });
      let beadCount = 0;
      for (let row = 0; row < edgeStream.edgeCount; row++) beadCount += edgeStream.beads(row).length;
      grow.push({ count: beadCount, cap: beadCap, set: setBeadCap });
    }

    // Node/Interior/Port + Label/PortName bytes are aggregated from every node row's own
    // dedicated stream frame (node-stream-blocks.ts) — grow nodeCap/portCap off that
    // aggregate's counts, independent of edge/bead stream arrival.
    const decodedNode = getNodeFrame();
    if (decodedNode) {
      grow.push(
        { count: decodedNode.nodeCount, cap: nodeCap, set: setNodeCap },
        { count: decodedNode.portCount, cap: portCap, set: setPortCap },
      );
    }

    for (const g of grow) {
      if (g.count > g.cap) g.set(Math.ceil(g.count * 1.5));
    }
  });

  return (
    <>
      <BufferCamera cameraRef={cameraRef} />
      <BeadInstances capacity={beadCap} />
      <NodeInstances capacity={nodeCap} />
      <PortInstances capacity={portCap} />
      <InteriorBeadInstances capacity={nodeCap * INTERIOR_SLOTS_PER_NODE} />
      <SelectionHighlight />
      <HoverHighlight />
      <SphereRings />
      <EdgeTubes     capacity={edgeCap} layoutLinkCapacity={layoutLinkCap} />
    </>
  );
}
