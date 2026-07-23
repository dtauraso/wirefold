// BeadInstances.tsx — on-wire (transit) beads rendered along the wires, matching the JSON
// path's PulseBead (scene-beads.tsx). Split out of buffer-scene.tsx: pure buffer→GPU render,
// no state authority.

import { useRef } from "react";
import { useFrame } from "@react-three/fiber";
import * as THREE from "three";
import { beadStyleForValue } from "./bead-style";
import { getEdgeStreamAccessor } from "./edge-stream-blocks";

const BEAD_SPHERE_RADIUS = 4;
// On-wire (transit) bead ring tube ratio — mirror scene-beads.tsx's PulseBead
// (PULSE_BEAD_R=4, BEAD_RING_TUBE_RATIO=0.12) so the buffer path's transit beads
// match the JSON path's look exactly.
const BEAD_RING_TUBE_RATIO = 0.12;

// A bead draws only when Live=1 AND its value has a bead-style (0|1); a non-0/1 value has
// no style and is HIDDEN (excluded from the draw count), exactly like PulseBead. Two
// InstancedMeshes share one useFrame: a sphere body (R=4) and a torus ring (R=4, tube
// 4*0.12), both meshStandardMaterial with emissiveIntensity=0 like PulseBead. Color is
// value-driven via bead-style.ts (fill sphere + ring torus) — the same source the JSON
// on-wire/interior beads use, so buffer and JSON transit beads cannot visually diverge.
export function BeadInstances({ capacity }: { capacity: number }) {
  const bodyRef = useRef<THREE.InstancedMesh>(null);
  const ringRef = useRef<THREE.InstancedMesh>(null);
  const matRef  = useRef(new THREE.Matrix4());
  const colRef  = useRef(new THREE.Color());

  useFrame(() => {
    const body = bodyRef.current;
    const ring = ringRef.current;
    if (!body || !ring) return;

    // Every edge's own bead rows are aggregated across ALL edge cells (memory/
    // feedback_no_single_writer_bridge.md) — there is no single shared Bead frame; each
    // edge's wire carries only its own beads. null (no per-edge stream frame has arrived
    // yet — e.g. before Go's first emit) means zero beads to draw this frame.
    const edgeStream = getEdgeStreamAccessor();
    let slot = 0;
    if (edgeStream) {
      for (let row = 0; row < edgeStream.edgeCount && slot < capacity; row++) {
        for (const b of edgeStream.beads(row)) {
          if (slot >= capacity) break;
          const style = beadStyleForValue(b.val);
          if (!style) continue; // non-0/1 value → hide (never paint a fallback)
          matRef.current.setPosition(b.x, b.y, b.z);
          body.setMatrixAt(slot, matRef.current);
          ring.setMatrixAt(slot, matRef.current);
          body.setColorAt(slot, colRef.current.set(style.fill));
          ring.setColorAt(slot, colRef.current.set(style.ring));
          slot++;
        }
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
