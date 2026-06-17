// NavGuides.tsx — decorative 3D navigation overlays:
//   Prism: axis-aligned bounding box edges + corner vertices over all nodes.
//   ArcballSphere: two perpendicular tori centered on mid-depth focus point,
//     radius scaled to ARCBALL_FILL * camera-to-focus distance, updated each frame.
// Both are purely decorative: raycast disabled, depthWrite false, transparent.

import React, { useMemo } from "react";
import * as THREE from "three";
import type { RFNode, NodeData } from "../types";
import { nodeWorldPos, nodeRadius } from "./geometry-helpers";
import { useNodeGeometryStore } from "./node-geometry";
import { computeContentSphere } from "./interaction-controls";
import { PanGuide } from "./PanGuide";
import { ZoomGuide } from "./ZoomGuide";

// ---------------------------------------------------------------------------
// Prism — axis-aligned bounding box of all node world positions.
// 12 line-segment edges + 8 corner spheres.
// ---------------------------------------------------------------------------

function Prism({ nodes }: { nodes: RFNode<NodeData>[] }) {
  // Re-derive when Go streams updated geometry.
  useNodeGeometryStore((s) => s.geoms);

  const { edgesGeo, center, corners } = useMemo(() => {
    if (nodes.length < 2) return { edgesGeo: null, center: new THREE.Vector3(), corners: [] };

    let minX = Infinity, minY = Infinity, minZ = Infinity;
    let maxX = -Infinity, maxY = -Infinity, maxZ = -Infinity;
    for (const n of nodes) {
      const p = nodeWorldPos(n);
      if (p.x < minX) minX = p.x;
      if (p.y < minY) minY = p.y;
      if (p.z < minZ) minZ = p.z;
      if (p.x > maxX) maxX = p.x;
      if (p.y > maxY) maxY = p.y;
      if (p.z > maxZ) maxZ = p.z;
    }

    const w = maxX - minX;
    const h = maxY - minY;
    const d = maxZ - minZ;
    const cx = (minX + maxX) / 2;
    const cy = (minY + maxY) / 2;
    const cz = (minZ + maxZ) / 2;

    const box = new THREE.BoxGeometry(w || 1, h || 1, d || 1);
    const geo = new THREE.EdgesGeometry(box);
    box.dispose();

    const hw = w / 2;
    const hh = h / 2;
    const hd = d / 2;
    const cornerPositions: THREE.Vector3[] = [
      new THREE.Vector3(cx - hw, cy - hh, cz - hd),
      new THREE.Vector3(cx + hw, cy - hh, cz - hd),
      new THREE.Vector3(cx - hw, cy + hh, cz - hd),
      new THREE.Vector3(cx + hw, cy + hh, cz - hd),
      new THREE.Vector3(cx - hw, cy - hh, cz + hd),
      new THREE.Vector3(cx + hw, cy - hh, cz + hd),
      new THREE.Vector3(cx - hw, cy + hh, cz + hd),
      new THREE.Vector3(cx + hw, cy + hh, cz + hd),
    ];

    return {
      edgesGeo: geo,
      center: new THREE.Vector3(cx, cy, cz),
      corners: cornerPositions,
    };
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [nodes]);

  if (!edgesGeo || nodes.length < 2) return null;

  return (
    <>
      <lineSegments geometry={edgesGeo} position={[center.x, center.y, center.z]} raycast={() => null}>
        <lineBasicMaterial color="#88aacc" transparent opacity={0.3} depthWrite={false} />
      </lineSegments>
      {corners.map((pos, i) => (
        <mesh key={i} position={[pos.x, pos.y, pos.z]} raycast={() => null}>
          <sphereGeometry args={[4, 8, 8]} />
          <meshBasicMaterial color="#88aacc" transparent opacity={0.3} depthWrite={false} />
        </mesh>
      ))}
    </>
  );
}

// ---------------------------------------------------------------------------
// ArcballSphere — two perpendicular tori tracking the arcball focus point.
// Major radius = ARCBALL_FILL * camera-to-focus distance; updated every frame.
// ---------------------------------------------------------------------------

function ArcballSphere({ nodes }: { nodes: RFNode<NodeData>[] }) {
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
  // Two more tori rotated 45° from the existing pair (about the same X axis): 45° and 135°,
  // so the four great circles sit at 0/45/90/135.
  const rotC = useMemo(() => new THREE.Euler(Math.PI / 4, 0, 0), []);
  const rotD = useMemo(() => new THREE.Euler((3 * Math.PI) / 4, 0, 0), []);
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
      <mesh geometry={geoA} rotation={rotC} raycast={() => null}>
        <meshBasicMaterial color="#cc8844" transparent opacity={0.4} depthWrite={false} />
      </mesh>
      <mesh geometry={geoA} rotation={rotD} raycast={() => null}>
        <meshBasicMaterial color="#cc8844" transparent opacity={0.4} depthWrite={false} />
      </mesh>
    </group>
  );
}

// ---------------------------------------------------------------------------
// NavGuides — combined export
// ---------------------------------------------------------------------------

export function NavGuides({ nodes, selectedId }: { nodes: RFNode<NodeData>[]; selectedId?: string | null }) {
  return (
    <>
      <Prism nodes={nodes} />
      <ArcballSphere nodes={nodes} />
      <PanGuide nodes={nodes} />
      <ZoomGuide nodes={nodes} selectedId={selectedId ?? null} />
    </>
  );
}
