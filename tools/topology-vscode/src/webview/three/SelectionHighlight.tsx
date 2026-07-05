// SelectionHighlight.tsx — SelectionHighlight (the selected node + every node ON THE SURFACE
// of its sphere) and HoverHighlight (the Go-hovered node's ring). Split out of
// buffer-scene.tsx: pure buffer→GPU render, no state authority.

import { useRef } from "react";
import { useFrame } from "@react-three/fiber";
import * as THREE from "three";
import { getLatestSnapshot } from "../snapshot-buffer";
import { decodeSnapshot } from "./buffer-decode";
import { surfaceRowSet, type EdgeAdj, type SelMode } from "./buffer-surface";
import {
  readNodeCX, readNodeCY, readNodeCZ, readNodeRadius, readNodeSelected,
  readEdgeSrcNodeRow, readEdgeDstNodeRow, readNodeHovered, readOverlaySelMode,
} from "../../schema/buffer-layout";
import { NODE_SPHERE_RADIUS, HOVER_COLOR, HOVER_RING_TUBE_RATIO } from "./buffer-scene-shared";

// Selection-highlight pool geometry (SelectionHighlight, built at radius=1 and scaled by
// the node's radius via g.scale.setScalar(r)). Matches GraphNode's selected border
// (r*0.14 thick) and NODE_HALO_R_RATIO=1.45 halo sphere.
const SELECTION_RING_TUBE_RATIO = 0.14;
const SELECTION_RING_RADIAL_SEGMENTS = 8;
const SELECTION_RING_TUBULAR_SEGMENTS = 32;
const SELECTION_HALO_R_RATIO = 1.45;
const SELECTION_HALO_WIDTH_SEGMENTS = 16;
const SELECTION_HALO_HEIGHT_SEGMENTS = 16;

// Highlight drawn around the Go-selected node AND every node ON THE SURFACE of that
// node's sphere (pre-branch parity). Matches the scene-graph.tsx GraphNode look: a thick
// yellow torus ring + an orange halo sphere, identical for the selected node and each
// on-surface node. Go OWNS the selection (the Selected column marks the one selected
// node, SelMode picks the mode); the on-surface set is pure edge-graph topology derived
// here from the Edge block's src/dst node-row adjacency via surfaceRowSet — no TS
// selection state, no geometry/timing logic. A pooled set of highlight groups (one per
// highlighted node) is repositioned each frame; unused pool slots hide.
const HIGHLIGHT_POOL = 32;

export function SelectionHighlight({ capacity }: { capacity: number }) {
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
          // Torus: args=[1, SELECTION_RING_TUBE_RATIO, SELECTION_RING_RADIAL_SEGMENTS,
          // SELECTION_RING_TUBULAR_SEGMENTS]; halo sphere: args=[SELECTION_HALO_R_RATIO,
          // SELECTION_HALO_WIDTH_SEGMENTS, SELECTION_HALO_HEIGHT_SEGMENTS].
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
        <group key={`highlight-slot-${i}`} ref={(el) => { groupRefs.current[i] = el; }} visible={false}>
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
export function HoverHighlight() {
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
