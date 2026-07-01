// scene-beads.tsx — PulseBead and InteriorBeads: wire and interior bead renderers.
import React, { useRef } from "react";
import { useFrame } from "@react-three/fiber";
import * as THREE from "three";
import { beadStyleForValue } from "./bead-style";
import { getPulseMapForEdge } from "./pulse-state";
import { getInteriorBeadMap, interiorBeadKey } from "./interior-bead-state";
import { getNodeStatusMap } from "./node-status-state";
import { useEdgeGeometryStore } from "./edge-geometry";

// PulseBead: draws the in-flight beads ON a wire at their Go-owned fractional progress.
// A wire may carry MULTIPLE beads at once (a clock-paced train): getPulseMap() is keyed
// by `${edgeId}:${beadID}`, so each in-flight bead on this edge is its own map entry.
// Go owns the clock and each bead's fraction; this component PLOTS ONLY — each frame it
// reads every map entry whose edgeId === this edge and places one sprite at
// lerp(start, end, frac) on the SAME segment SingleEdgeTube draws (Go's wireSegment from
// useEdgeGeometryStore). Because beads and tube share one segment source, every bead is
// provably on the line and rides the wire as the node moves — no lag, no drift. No curve
// sampling beyond the linear lerp, no clock, no delivery message (MODEL.md).
//
// Imperative pool: PULSE_POOL bead groups are mounted once; each frame we map the edge's
// live entries onto pool slots (placed/colored/visible) and hide the unused slots. No
// React state per bead — placement is pure useFrame mutation, off React's render path.
const PULSE_POOL = 16;

export function PulseBead({
  edgeId,
}: {
  edgeId: string;
}) {
  const slotRefs = useRef<(THREE.Group | null)[]>([]);
  const sphereMatRefs = useRef<(THREE.MeshStandardMaterial | null)[]>([]);
  const torusMatRefs = useRef<(THREE.MeshStandardMaterial | null)[]>([]);
  // SAME source SingleEdgeTube subscribes to — beads and tube cannot diverge.
  const seg = useEdgeGeometryStore((s) => s.segments[edgeId]);

  useFrame(() => {
    const slots = slotRefs.current;
    // Gather this edge's live beads (one map entry per in-flight bead on this wire).
    // No segment yet (startup race) → hide all, no crash.
    let slot = 0;
    if (seg) {
      // Per-edge slice: only this wire's beads (O(beads-on-this-edge)), not a
      // full scan of every in-flight bead across all edges.
      for (const pulse of getPulseMapForEdge(edgeId).values()) {
        if (slot >= PULSE_POOL) break; // pool exhausted — extra beads wait a frame
        const g = slots[slot];
        if (!g) { slot++; continue; }
        // A non-0/1 value has no style → leave this slot hidden.
        const style = beadStyleForValue(pulse.value);
        if (!style) { g.visible = false; slot++; continue; }
        // Place at the Go-supplied fraction along the Go-supplied segment.
        const f = pulse.frac;
        g.position.set(
          seg.start.x + f * (seg.end.x - seg.start.x),
          seg.start.y + f * (seg.end.y - seg.start.y),
          seg.start.z + f * (seg.end.z - seg.start.z),
        );
        sphereMatRefs.current[slot]?.color.set(style.fill);
        torusMatRefs.current[slot]?.color.set(style.ring);
        g.visible = true;
        slot++;
      }
    }
    // Hide any pool slots not claimed this frame.
    for (let i = slot; i < PULSE_POOL; i++) {
      const g = slots[i];
      if (g) g.visible = false;
    }
  });

  return (
    <>
      {Array.from({ length: PULSE_POOL }, (_, i) => (
        <group key={i} ref={(el) => { slotRefs.current[i] = el; }} visible={false}>
          <mesh raycast={() => null}>
            <sphereGeometry args={[4, 16, 16]} />
            <meshStandardMaterial ref={(el) => { sphereMatRefs.current[i] = el; }} emissiveIntensity={0} />
          </mesh>
          <mesh raycast={() => null}>
            <torusGeometry args={[4, 4 * 0.12, 8, 24]} />
            <meshStandardMaterial ref={(el) => { torusMatRefs.current[i] = el; }} emissiveIntensity={0} />
          </mesh>
        </group>
      ))}
    </>
  );
}

// InteriorBeads: renders node 1's 2x2 interior buffer from the live node-bead
// stream as CHILDREN of the node group (mounted inside GraphNode). Go owns the slot
// offsets (NODE-LOCAL, relative to the node center) and the present/absent state;
// this component PLOTS ONLY — it reads getInteriorBeadMap() imperatively each frame
// and places each PRESENT slot's mesh at its Go-supplied local offset, hiding empty
// (popped) slots. Because the mesh is a child of the node group, its world position
// = node center + offset is composed by the scene graph, so the beads ride the node
// on drag with no re-emit. TS computes no geometry (no interior layout math).
// Discrete positions this phase (beads snap; no slide yet).
const INTERIOR_SLOTS: { row: number; col: number }[] = [
  { row: 0, col: 0 }, { row: 0, col: 1 },
  { row: 1, col: 0 }, { row: 1, col: 1 },
];
const INTERIOR_BEAD_R = 5;

function InteriorSlotBead({ nodeId, row, col }: { nodeId: string; row: number; col: number }) {
  const groupRef = useRef<THREE.Group>(null);
  const sphereMatRef = useRef<THREE.MeshStandardMaterial>(null);
  const torusMatRef = useRef<THREE.MeshStandardMaterial>(null);

  useFrame(() => {
    const group = groupRef.current;
    if (!group) return;
    const slot = getInteriorBeadMap().get(interiorBeadKey(nodeId, row, col));
    // Hidden until Go has streamed this slot AND it is present (popped slots carry
    // present=false → hide). No geometry: place the mesh at Go's NODE-LOCAL offset.
    // The parent node group supplies the center, so this is the offset verbatim.
    if (!slot || !slot.present) {
      group.visible = false;
      return;
    }
    group.position.set(slot.pos.x, slot.pos.y, slot.pos.z);
    // A non-0/1 value has no style → hide rather than paint a fallback.
    const style = beadStyleForValue(slot.value);
    if (!style) {
      group.visible = false;
      return;
    }
    sphereMatRef.current?.color.set(style.fill);
    torusMatRef.current?.color.set(style.ring);
    group.visible = true;
  });

  return (
    <group ref={groupRef} visible={false}>
      <mesh raycast={() => null}>
        <sphereGeometry args={[INTERIOR_BEAD_R, 16, 16]} />
        <meshStandardMaterial ref={sphereMatRef} emissiveIntensity={0} />
      </mesh>
      <mesh raycast={() => null}>
        <torusGeometry args={[INTERIOR_BEAD_R, INTERIOR_BEAD_R * 0.12, 8, 24]} />
        <meshStandardMaterial ref={torusMatRef} emissiveIntensity={0} />
      </mesh>
    </group>
  );
}

// MissedBeadMarkers: renders the missed/ignored bead just OUTSIDE a node while Go
// reports a firing error (node-status torusRed=true). Go streams the WORLD position
// (x/y/z) and the ignored bead's value; this component PLOTS ONLY — each frame it
// reads getNodeStatusMap() imperatively, and for every entry whose torusRed=true
// places one pooled marker at the Go-supplied world position, colored by the missed
// value like any bead. torusRed=false → the entry is skipped → the marker hides. No
// geometry, no timer: the revert rides the next node-status event Go sends. Mounted
// at scene level (not a child of any node group) because the position is world-space.
const MISSED_POOL = 16;
// Deliberately larger than a normal in-flight bead so the missed bead reads as
// "the one that got ignored," not just another bead passing by.
const MISSED_BEAD_R = 9;

export function MissedBeadMarkers() {
  const slotRefs = useRef<(THREE.Group | null)[]>([]);
  const sphereMatRefs = useRef<(THREE.MeshStandardMaterial | null)[]>([]);
  const torusMatRefs = useRef<(THREE.MeshStandardMaterial | null)[]>([]);

  useFrame((state) => {
    const slots = slotRefs.current;
    // Cosmetic pulse (0..1) from the render clock, shared by all active markers so
    // they throb in sync with the errored ring. Styling only — decides no model state.
    const pulse = 0.5 + 0.5 * Math.sin(state.clock.elapsedTime * 8);
    let slot = 0;
    for (const status of getNodeStatusMap().values()) {
      if (!status.torusRed) continue;
      if (slot >= MISSED_POOL) break;
      const g = slots[slot];
      if (!g) { slot++; continue; }
      const style = beadStyleForValue(status.missedValue);
      if (!style) { g.visible = false; slot++; continue; }
      g.position.set(status.x, status.y, status.z);
      g.scale.setScalar(1.0 + 0.25 * pulse);
      const sm = sphereMatRefs.current[slot];
      if (sm) {
        sm.color.set(style.fill);
        // Self-glow in the bead's own color so the missed bead stands out from the
        // matte in-flight beads.
        sm.emissive.set(style.fill);
        sm.emissiveIntensity = 0.5 + 1.2 * pulse;
      }
      torusMatRefs.current[slot]?.color.set(style.ring);
      g.visible = true;
      slot++;
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

export function InteriorBeads({ nodeId }: { nodeId: string }) {
  return (
    <>
      {INTERIOR_SLOTS.map((s) => (
        <InteriorSlotBead key={`${s.row}:${s.col}`} nodeId={nodeId} row={s.row} col={s.col} />
      ))}
    </>
  );
}
