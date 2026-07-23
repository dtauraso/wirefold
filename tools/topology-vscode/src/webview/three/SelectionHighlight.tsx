// SelectionHighlight.tsx — SelectionHighlight (the single Go-selected node's ring +
// halo) and HoverHighlight (the Go-hovered node's ring). Split out of buffer-scene.tsx:
// pure buffer→GPU render, no state authority.

import { useRef } from "react";
import { useFrame } from "@react-three/fiber";
import * as THREE from "three";
import { getNodeFrameOrFallback } from "./node-stream-blocks";
import {
  readNodeCX, readNodeCY, readNodeCZ, readNodeRadius, readNodeSelected, readNodeHovered,
} from "../../schema/buffer-layout";
import { NODE_SPHERE_RADIUS, HOVER_COLOR, HOVER_RING_TUBE_RATIO } from "./buffer-scene-shared";

// Selection-highlight geometry (built at radius=1 and scaled by the node's radius via
// g.scale.setScalar(r)). Matches GraphNode's selected border (r*0.14 thick) and
// NODE_HALO_R_RATIO=1.45 halo sphere.
const SELECTION_RING_TUBE_RATIO = 0.14;
const SELECTION_RING_RADIAL_SEGMENTS = 8;
const SELECTION_RING_TUBULAR_SEGMENTS = 32;
const SELECTION_HALO_R_RATIO = 1.45;
const SELECTION_HALO_WIDTH_SEGMENTS = 16;
const SELECTION_HALO_HEIGHT_SEGMENTS = 16;

// Highlight drawn around the Go-selected node ONLY: a thick yellow torus ring + an
// orange halo sphere, matching the scene-graph.tsx GraphNode look. Go OWNS the
// selection (the Node block's Selected column marks the one selected node) — no TS
// selection state, no derived edge-graph traversal, no geometry/timing logic.
export function SelectionHighlight() {
  const groupRef = useRef<THREE.Group | null>(null);

  useFrame(() => {
    const g = groupRef.current;
    if (!g) return;

    const decoded = getNodeFrameOrFallback();
    let show = false;
    if (decoded) {
      const { nodeCount, nodeView } = decoded;

      // Find the selected row (at most one).
      let selectedRow = -1;
      for (let i = 0; i < nodeCount; i++) {
        if (readNodeSelected(nodeView, i)) { selectedRow = i; break; }
      }

      if (selectedRow >= 0) {
        const r = readNodeRadius(nodeView, selectedRow) || NODE_SPHERE_RADIUS;
        g.position.set(
          readNodeCX(nodeView, selectedRow),
          readNodeCY(nodeView, selectedRow),
          readNodeCZ(nodeView, selectedRow),
        );
        // Scale so child geometries built at radius=1 match r.
        // Torus: args=[1, SELECTION_RING_TUBE_RATIO, SELECTION_RING_RADIAL_SEGMENTS,
        // SELECTION_RING_TUBULAR_SEGMENTS]; halo sphere: args=[SELECTION_HALO_R_RATIO,
        // SELECTION_HALO_WIDTH_SEGMENTS, SELECTION_HALO_HEIGHT_SEGMENTS].
        g.scale.setScalar(r);
        show = true;
      }
    }
    g.visible = show;
  });

  return (
    <group ref={groupRef} visible={false}>
      {/* Yellow torus ring — matches GraphNode selected border: r*0.14 thick */}
      <mesh raycast={() => null} frustumCulled={false}>
        <torusGeometry args={[1, SELECTION_RING_TUBE_RATIO, SELECTION_RING_RADIAL_SEGMENTS, SELECTION_RING_TUBULAR_SEGMENTS]} />
        <meshStandardMaterial color="#ffcc00" emissive="#ffcc00" emissiveIntensity={0.3} />
      </mesh>
      {/* Orange halo sphere — matches GraphNode NODE_HALO_R_RATIO=1.45 */}
      <mesh raycast={() => null} frustumCulled={false}>
        <sphereGeometry args={[SELECTION_HALO_R_RATIO, SELECTION_HALO_WIDTH_SEGMENTS, SELECTION_HALO_HEIGHT_SEGMENTS]} />
        <meshBasicMaterial
          color="#ff5a00"
          transparent
          opacity={0.5}
          side={THREE.DoubleSide}
          depthWrite={false}
        />
      </mesh>
    </group>
  );
}

// Hover highlight drawn around the Go-hovered node (the Hovered column). Mirrors the
// pre-branch GraphNode hover look: a #aaddff border ring thickened to r*0.14 (same
// thickness the selection ring uses). Go OWNS hover; this reads the Hovered column and
// draws a single ring — no TS hover state. SELECTION TAKES PRECEDENCE: when the hovered
// node is the selected node (which SelectionHighlight already rings in yellow), the
// hover ring is suppressed so the selection color wins. Only one node can be hovered at
// a time, so a single mesh (no pool) suffices.
export function HoverHighlight() {
  const ringRef = useRef<THREE.Mesh>(null);

  useFrame(() => {
    const ring = ringRef.current;
    if (!ring) return;

    const decoded = getNodeFrameOrFallback();
    let show = false;
    if (decoded) {
      const { nodeCount, nodeView } = decoded;

      // Hovered node row (at most one).
      let hoveredRow = -1;
      for (let i = 0; i < nodeCount; i++) {
        if (readNodeHovered(nodeView, i)) { hoveredRow = i; break; }
      }

      if (hoveredRow >= 0) {
        // Suppress if the hovered node is the selected node — the ring
        // SelectionHighlight already draws there wins.
        const suppressed = readNodeSelected(nodeView, hoveredRow) !== 0;
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
