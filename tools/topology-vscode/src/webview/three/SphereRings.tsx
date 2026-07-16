// SphereRings.tsx — "show the sphere" visualization: for the currently-SELECTED node, two
// thin see-through great-circle tori are drawn AT its center showing its own reach-sphere
// boundary. Split out of buffer-scene.tsx: pure buffer→GPU render, no state authority
// beyond the geometry-rebuild-on-change React state SphereRings already held.

import { useRef, useState, useMemo, useEffect } from "react";
import { useFrame } from "@react-three/fiber";
import * as THREE from "three";
import { getLatestSnapshot } from "../snapshot-buffer";
import { decodeSnapshot } from "./buffer-decode";
import {
  readNodeCX, readNodeCY, readNodeCZ, readNodeRadius, readNodeSelected, readNodeSphereR,
  readNodeVRX, readNodeVRY, readNodeVRZ, readNodeFRX, readNodeFRY, readNodeFRZ,
} from "../../schema/buffer-layout";
import { NODE_SPHERE_RADIUS, NORMAL_DEGENERATE_EPS, SPHERE_RING_MIN_RADIUS, nodeRowColors } from "./buffer-scene-shared";

// Mirrors the pre-branch SphereRing (scene-graph.tsx) EXACTLY: major radius R = the
// selected node's Go-streamed sphereR (buffer SphereR column), tube = max(0.5,
// radius*0.08), two tori oriented by the node's two ring-plane normals (VR vertical, FR
// flat), material = node stroke color, emissiveIntensity 0.25, opacity 0.55, depthWrite
// false, raycast disabled (purely decorative — clicks pass through to the node inside).
// Drawn only when a selection exists AND that node's SphereR is Go-streamed non-degenerate
// (SPHERE_RING_MIN_RADIUS filters out nodes that don't center a sphere at all).
const SPHERE_RING_EMISSIVE_INTENSITY = 0.25;
const SPHERE_RING_OPACITY = 0.55;
const SPHERE_RING_TUBE_RATIO = 0.08; // pre-branch: nodeRadius(owner) * 0.08
const SPHERE_RING_TUBE_MIN = 0.5;
const SPHERE_RING_RADIAL_SEGMENTS = 12;
const SPHERE_RING_TUBULAR_SEGMENTS = 96;
const _sphereRingDefaultNormal = new THREE.Vector3(0, 0, 1); // torusGeometry lies in XY (normal +Z)

interface OwnerRing {
  row: number; // owner node's buffer node-row index — stable identity for React keys
  cx: number; cy: number; cz: number;
  R: number; tube: number;
  vrx: number; vry: number; vrz: number;
  frx: number; fry: number; frz: number;
  color: string;
}

// One owner's pair of great-circle tori. Geometry + orientation quaternions are rebuilt
// only when the owner's R/tube/normals change (keyed useMemo) — not every frame.
function SphereRingBuf({ ring }: { ring: OwnerRing }) {
  const { geo, vrQ, frQ } = useMemo(() => {
    const _geo = new THREE.TorusGeometry(ring.R, ring.tube, SPHERE_RING_RADIAL_SEGMENTS, SPHERE_RING_TUBULAR_SEGMENTS);
    const vrN = new THREE.Vector3(ring.vrx, ring.vry, ring.vrz);
    if (vrN.lengthSq() < NORMAL_DEGENERATE_EPS) vrN.set(0, 0, 1); else vrN.normalize();
    const frN = new THREE.Vector3(ring.frx, ring.fry, ring.frz);
    if (frN.lengthSq() < NORMAL_DEGENERATE_EPS) frN.set(1, 0, 0); else frN.normalize();
    return {
      geo: _geo,
      vrQ: new THREE.Quaternion().setFromUnitVectors(_sphereRingDefaultNormal, vrN),
      frQ: new THREE.Quaternion().setFromUnitVectors(_sphereRingDefaultNormal, frN),
    };
  }, [ring.R, ring.tube, ring.vrx, ring.vry, ring.vrz, ring.frx, ring.fry, ring.frz]);

  // R3F does not auto-dispose an imperatively-passed geometry; dispose on rebuild/unmount.
  useEffect(() => () => { geo.dispose(); }, [geo]);

  return (
    <group position={[ring.cx, ring.cy, ring.cz]}>
      <mesh geometry={geo} quaternion={[vrQ.x, vrQ.y, vrQ.z, vrQ.w]} raycast={() => null} frustumCulled={false}>
        <meshStandardMaterial
          color={ring.color}
          emissive={ring.color}
          emissiveIntensity={SPHERE_RING_EMISSIVE_INTENSITY}
          transparent
          opacity={SPHERE_RING_OPACITY}
          depthWrite={false}
        />
      </mesh>
      <mesh geometry={geo} quaternion={[frQ.x, frQ.y, frQ.z, frQ.w]} raycast={() => null} frustumCulled={false}>
        <meshStandardMaterial
          color={ring.color}
          emissive={ring.color}
          emissiveIntensity={SPHERE_RING_EMISSIVE_INTENSITY}
          transparent
          opacity={SPHERE_RING_OPACITY}
          depthWrite={false}
        />
      </mesh>
    </group>
  );
}

function sameRings(a: OwnerRing[], b: OwnerRing[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    const x = a[i]!;
    const y = b[i]!;
    if (
      x.row !== y.row ||
      x.cx !== y.cx || x.cy !== y.cy || x.cz !== y.cz ||
      x.R !== y.R || x.tube !== y.tube ||
      x.vrx !== y.vrx || x.vry !== y.vry || x.vrz !== y.vrz ||
      x.frx !== y.frx || x.fry !== y.fry || x.frz !== y.frz ||
      x.color !== y.color
    ) {
      return false;
    }
  }
  return true;
}

export function SphereRings() {
  const [rings, setRings] = useState<OwnerRing[]>([]);
  const prevRef = useRef<OwnerRing[]>([]);

  useFrame(() => {
    const snap = getLatestSnapshot();
    const decoded = snap ? decodeSnapshot(snap) : null;
    const next: OwnerRing[] = [];
    if (decoded) {
      const { nodeCount, nodeView } = decoded;

      // Selected row (at most one) — the ONLY row this draws a sphere ring for.
      let selectedRow = -1;
      for (let i = 0; i < nodeCount; i++) {
        if (readNodeSelected(nodeView, i)) { selectedRow = i; break; }
      }

      if (selectedRow >= 0) {
        const row = selectedRow;
        // R = Go-streamed reach radius (sphereR); fall back to node radius pre-emit.
        const radius = readNodeRadius(nodeView, row) || NODE_SPHERE_RADIUS;
        const R = readNodeSphereR(nodeView, row) || radius;
        if (R >= SPHERE_RING_MIN_RADIUS) {
          const tube = Math.max(SPHERE_RING_TUBE_MIN, radius * SPHERE_RING_TUBE_RATIO);
          const ring: OwnerRing = {
            row,
            cx: readNodeCX(nodeView, row), cy: readNodeCY(nodeView, row), cz: readNodeCZ(nodeView, row),
            R, tube,
            vrx: readNodeVRX(nodeView, row), vry: readNodeVRY(nodeView, row), vrz: readNodeVRZ(nodeView, row),
            frx: readNodeFRX(nodeView, row), fry: readNodeFRY(nodeView, row), frz: readNodeFRZ(nodeView, row),
            color: nodeRowColors(nodeView, row).stroke,
          };
          next.push(ring);
        }
      }
    }
    // Rebuild only when the ring set / geometry / color actually changed.
    if (!sameRings(prevRef.current, next)) {
      prevRef.current = next;
      setRings(next);
    }
  });

  return (
    <>
      {rings.map((ring) => (
        <SphereRingBuf key={ring.row} ring={ring} />
      ))}
    </>
  );
}
