// InteriorBeadInstances.tsx — interior (held) beads rendered INSIDE each node, matching the
// JSON path's InteriorBeads (scene-beads.tsx). Split out of buffer-scene.tsx: pure
// buffer→GPU render, no state authority.

import { useRef } from "react";
import { useFrame } from "@react-three/fiber";
import * as THREE from "three";
import { getLatestSnapshot } from "../snapshot-buffer";
import { decodeSnapshot, INTERIOR_SLOTS_PER_NODE } from "./buffer-decode";
import { beadStyleForValue } from "./bead-style";
import {
  readNodeCX, readNodeCY, readNodeCZ,
  readInteriorPresent, readInteriorValue, readInteriorOX, readInteriorOY, readInteriorOZ,
} from "../../schema/buffer-layout";

// Interior (held) bead sphere radius + ring tube ratio — mirror scene-beads.tsx's
// InteriorSlotBead (INTERIOR_BEAD_R=5, BEAD_RING_TUBE_RATIO=0.12) so the buffer path's
// interior beads match the JSON path's look exactly.
const INTERIOR_BEAD_R = 5;
const INTERIOR_RING_TUBE_RATIO = 0.12;

// The Interior block carries a fixed INTERIOR_SLOTS_PER_NODE rows per node (row =
// nodeRow*slots + slot); a slot draws only when Present=1 AND its value has a bead-style
// (0|1). World position = the node's buffer center + the Go-owned NODE-LOCAL slot offset
// (OX/OY/OZ) — the buffer path has no node group to inherit, so we add the center here (the
// JSON path composes it via the scene graph). Color is value-driven via bead-style.ts (fill
// sphere + ring torus), the same source the JSON interior/edge beads use, so they cannot
// visually diverge.
export function InteriorBeadInstances({ capacity }: { capacity: number }) {
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
