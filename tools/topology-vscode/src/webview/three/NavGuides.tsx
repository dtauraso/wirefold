// NavGuides.tsx — decorative 3D navigation overlays:
//   Prism: axis-aligned bounding box edges + corner vertices over all nodes.
//   ArcballSphere: two perpendicular tori centered on mid-depth focus point,
//     radius scaled to ARCBALL_FILL * camera-to-focus distance, updated each frame.
// Both are purely decorative: raycast disabled, depthWrite false, transparent.

import React, { useRef, useMemo } from "react";
import { useThree, useFrame } from "@react-three/fiber";
import * as THREE from "three";
import type { RFNode, NodeData } from "../types";
import { nodeWorldPos } from "./geometry-helpers";
import { useNodeGeometryStore } from "./node-geometry";

// Fraction of camera-to-focus distance used as the arcball sphere radius.
// Kept in sync with the arcball radius used in interaction-controls.ts.
const ARCBALL_FILL = 0.75;

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
  const { camera } = useThree();

  // Unit-radius torus geometries — scale the mesh each frame instead of
  // rebuilding geometry. Major radius 1, tube radius 0.015, segments 64/16.
  const torusGeoA = useMemo(() => new THREE.TorusGeometry(1, 0.015, 16, 64), []);
  const torusGeoB = useMemo(() => new THREE.TorusGeometry(1, 0.015, 16, 64), []);

  const refA = useRef<THREE.Mesh>(null);
  const refB = useRef<THREE.Mesh>(null);

  // Rotation for torus B: lies in XZ plane (rotate torusGeo's default XY plane 90° around X).
  const rotB = useMemo(() => new THREE.Euler(Math.PI / 2, 0, 0), []);

  useFrame(() => {
    const meshA = refA.current;
    const meshB = refB.current;
    if (!meshA || !meshB) return;

    const forward = camera.getWorldDirection(new THREE.Vector3());

    // Compute per-node depth along camera forward axis.
    let minDepth = Infinity;
    let maxDepth = -Infinity;
    for (const n of nodes) {
      const wp = nodeWorldPos(n);
      const depth = forward.dot(wp.clone().sub(camera.position));
      if (depth < minDepth) minDepth = depth;
      if (depth > maxDepth) maxDepth = depth;
    }

    const midDepth = nodes.length > 0
      ? Math.max((minDepth + maxDepth) / 2, 10)
      : 100;

    const focus = camera.position.clone().add(forward.clone().multiplyScalar(midDepth));
    const R = ARCBALL_FILL * camera.position.distanceTo(focus);

    meshA.position.copy(focus);
    meshA.scale.setScalar(R);

    meshB.position.copy(focus);
    meshB.scale.setScalar(R);
  });

  return (
    <>
      <mesh ref={refA} geometry={torusGeoA} raycast={() => null}>
        <meshBasicMaterial color="#cc8844" transparent opacity={0.25} depthWrite={false} />
      </mesh>
      <mesh ref={refB} geometry={torusGeoB} rotation={rotB} raycast={() => null}>
        <meshBasicMaterial color="#cc8844" transparent opacity={0.25} depthWrite={false} />
      </mesh>
    </>
  );
}

// ---------------------------------------------------------------------------
// NavGuides — combined export
// ---------------------------------------------------------------------------

export function NavGuides({ nodes }: { nodes: RFNode<NodeData>[] }) {
  return (
    <>
      <Prism nodes={nodes} />
      <ArcballSphere nodes={nodes} />
    </>
  );
}
