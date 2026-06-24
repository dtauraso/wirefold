// NavGuides.tsx — decorative 3D navigation overlays:
//   PolarSphere: two perpendicular tori centered on mid-depth focus point,
//     radius scaled to ARCBALL_FILL * camera-to-focus distance, updated each frame.
// Purely decorative: raycast disabled, depthWrite false, transparent.

import React, { useMemo } from "react";
import * as THREE from "three";
import type { RFNode, NodeData } from "../types";
import { nodeRadius } from "./geometry-helpers";
import { useNodeGeometryStore } from "./node-geometry";
import { computeContentSphere } from "./interaction-controls";
import { useCameraStore } from "./camera-store";

// ---------------------------------------------------------------------------
// PolarSphere — two perpendicular tori tracking the polar rotation-sphere center.
// Major radius = ARCBALL_FILL * camera-to-focus distance; updated every frame.
// ---------------------------------------------------------------------------

function PolarSphere({ nodes }: { nodes: RFNode<NodeData>[] }) {
  // Re-derive when Go streams node geometry (positions change → content sphere moves).
  useNodeGeometryStore((s) => s.geoms);
  const sceneToriVisible = useCameraStore((s) => s.sceneToriVisible);

  // WORLD-FIXED content sphere (= the arcball, matching interaction-controls), so it
  // zooms WITH the diagram. Tube thickness matches the node spheres' tori
  // (scene-content SphereRing: max(0.5, nodeRadius·0.08)).
  const cs = computeContentSphere(nodes);
  const tube = nodes.length > 0 ? Math.max(0.5, nodeRadius(nodes[0]) * 0.08) : 1;
  // Build geometry ONLY when the sphere actually changes (rounded radius/tube), not on
  // every render — rebuilding each frame under node-geometry churn made the tori flicker
  // and effectively disappear.
  const radiusKey = Math.round(cs.radius);
  const tubeKey = Math.round(tube * 10);
  const { geoA, geoB } = useMemo(
    () => ({
      geoA: new THREE.TorusGeometry(radiusKey, tubeKey / 10, 12, 96),
      geoB: new THREE.TorusGeometry(radiusKey, tubeKey / 10, 12, 96),
    }),
    [radiusKey, tubeKey],
  );
  const rotB = useMemo(() => new THREE.Euler(Math.PI / 2, 0, 0), []);

  // Handholds: 4 grab points per torus, 90° apart. Grabbing one starts a CONSTRAINED
  // rotation — the first two cursor points lock the rotation disk (see
  // interaction-handlers.ts "handhold-rotating"). These are the only PICKABLE part of
  // the nav overlay (the tori stay raycast-disabled); each carries userData.handhold.
  // Placed in the torus's own local frame: geoA lies in XY (handholds at z=0), geoB is
  // the same ring under rotB, so its handhold group shares that rotation.
  const hhAngles = [0, Math.PI / 2, Math.PI, (3 * Math.PI) / 2];
  const hhRadius = Math.max(radiusKey * 0.04, 3); // grabbable, scales with the sphere
  const handholds = (rotation?: THREE.Euler) => (
    <group rotation={rotation}>
      {hhAngles.map((a, i) => (
        <mesh key={i} position={[radiusKey * Math.cos(a), radiusKey * Math.sin(a), 0]} userData={{ handhold: true }}>
          <sphereGeometry args={[hhRadius, 16, 16]} />
          <meshStandardMaterial color="#cc8844" emissive="#cc8844" emissiveIntensity={0.6} transparent opacity={0.9} />
        </mesh>
      ))}
    </group>
  );

  // Polar frame axis markers: pole (+y, green) and the two equatorial references
  // (+x = φ0, red; +z = φ90, blue), three.js X=red/Y=green/Z=blue. Camera-independent,
  // anchored at cs.center, decorative (not pickable). They show the layout's true frame
  // regardless of camera, and do NOT toggle with the scene tori.
  const poleLen = radiusKey * 1.3;
  const poleRadius = Math.max(radiusKey * 0.01, 1);
  const coneH = radiusKey * 0.12;
  const coneBaseR = radiusKey * 0.05;

  if (nodes.length < 1) return null;

  // WORLD-FIXED tori: the pole is the diagram's own top axis (world Y), so the horizontal torus
  // (geoB, normal world Y) is the diagram's equator — the polar frame is anchored to the
  // diagram, not the camera.
  const pos: [number, number, number] = [cs.center.x, cs.center.y, cs.center.z];
  return (
    <group position={pos}>
      {sceneToriVisible !== false && (
        <>
          <mesh geometry={geoA} raycast={() => null}>
            <meshBasicMaterial color="#cc8844" transparent opacity={0.4} depthWrite={false} />
          </mesh>
          <mesh geometry={geoB} rotation={rotB} raycast={() => null}>
            <meshBasicMaterial color="#cc8844" transparent opacity={0.4} depthWrite={false} />
          </mesh>
        </>
      )}
      {/* Grab handholds (4 per torus, 90° apart) — the pickable part of the overlay. */}
      {handholds()}
      {handholds(rotB)}
      {/* +Y pole (green): cylinder + cone arrowhead pointing world +y. */}
      <mesh position={[0, poleLen / 2, 0]} raycast={() => null}>
        <cylinderGeometry args={[poleRadius, poleRadius, poleLen, 12]} />
        <meshBasicMaterial color="#22dd55" depthWrite={false} />
      </mesh>
      <mesh position={[0, poleLen + coneH / 2, 0]} raycast={() => null}>
        <coneGeometry args={[coneBaseR, coneH, 12]} />
        <meshBasicMaterial color="#22dd55" depthWrite={false} />
      </mesh>
      {/* +X equatorial reference (φ=0, red): rotation [0,0,-π/2] turns +Y→+X. */}
      <mesh position={[poleLen / 2, 0, 0]} rotation={[0, 0, -Math.PI / 2]} raycast={() => null}>
        <cylinderGeometry args={[poleRadius, poleRadius, poleLen, 12]} />
        <meshBasicMaterial color="#dd3333" depthWrite={false} />
      </mesh>
      <mesh position={[poleLen + coneH / 2, 0, 0]} rotation={[0, 0, -Math.PI / 2]} raycast={() => null}>
        <coneGeometry args={[coneBaseR, coneH, 12]} />
        <meshBasicMaterial color="#dd3333" depthWrite={false} />
      </mesh>
      {/* +Z equatorial reference (φ=90°, blue): rotation [π/2,0,0] turns +Y→+Z. */}
      <mesh position={[0, 0, poleLen / 2]} rotation={[Math.PI / 2, 0, 0]} raycast={() => null}>
        <cylinderGeometry args={[poleRadius, poleRadius, poleLen, 12]} />
        <meshBasicMaterial color="#3366dd" depthWrite={false} />
      </mesh>
      <mesh position={[0, 0, poleLen + coneH / 2]} rotation={[Math.PI / 2, 0, 0]} raycast={() => null}>
        <coneGeometry args={[coneBaseR, coneH, 12]} />
        <meshBasicMaterial color="#3366dd" depthWrite={false} />
      </mesh>
    </group>
  );
}

// ---------------------------------------------------------------------------
// NavGuides — combined export
// ---------------------------------------------------------------------------

export function NavGuides({ nodes }: { nodes: RFNode<NodeData>[]; selectedId?: string | null }) {
  return (
    <>
      <PolarSphere nodes={nodes} />
    </>
  );
}
