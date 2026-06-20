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

// ---------------------------------------------------------------------------
// PolarSphere — two perpendicular tori tracking the polar rotation-sphere center.
// Major radius = ARCBALL_FILL * camera-to-focus distance; updated every frame.
// ---------------------------------------------------------------------------

function PolarSphere({ nodes }: { nodes: RFNode<NodeData>[] }) {
  // Re-derive when Go streams node geometry (positions change → content sphere moves).
  useNodeGeometryStore((s) => s.geoms);

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
  if (nodes.length < 1) return null;

  // WORLD-FIXED tori: the pole is the diagram's own top axis (world Y), so the horizontal torus
  // (geoB, normal world Y) is the diagram's equator — the polar frame is anchored to the
  // diagram, not the camera.
  const pos: [number, number, number] = [cs.center.x, cs.center.y, cs.center.z];
  return (
    <group position={pos}>
      <mesh geometry={geoA} raycast={() => null}>
        <meshBasicMaterial color="#cc8844" transparent opacity={0.4} depthWrite={false} />
      </mesh>
      <mesh geometry={geoB} rotation={rotB} raycast={() => null}>
        <meshBasicMaterial color="#cc8844" transparent opacity={0.4} depthWrite={false} />
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
