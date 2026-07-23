// PortInstances.tsx — port spheres: one small grabbable ball per buffer PORT row, matching
// the pre-branch PortSphere (scene-graph.tsx). Split out of buffer-scene.tsx: pure
// buffer→GPU render, no state authority.

import { useRef } from "react";
import { useFrame } from "@react-three/fiber";
import * as THREE from "three";
import { getNodeFrame } from "./node-stream-blocks";
import { readPortNodeRow, readPortPX, readPortPY, readPortPZ, readPortHovered } from "../../schema/buffer-layout";
import { BUFFER_PORT_TAG, PORT_SPHERE_R, PORT_HOVER_COLOR, PORT_HOVER_SCALE, nodeRowColors } from "./buffer-scene-shared";

// Placement mirrors PortSphere exactly: at nodeCenter + portDir*nodeRadius, where
// nodeCenter/nodeRadius come from the owning node's row (the port's NodeRow column) and
// portDir is the port's DX/DY/DZ surface direction. Color is the owning node's stroke (the
// same NODE_DEFS[kind].stroke NodeInstances uses for its ring). One InstancedMesh for all
// ports, tagged BUFFER_PORT_TAG for picking — instance i IS buffer port row i, so a raycast
// hit's instanceId is the port row Go resolves to a (node, port). No port position or
// identity is computed beyond this render placement; the numeric buffer owns it.
export function PortInstances({ capacity }: { capacity: number }) {
  const meshRef = useRef<THREE.InstancedMesh>(null);
  const matRef  = useRef(new THREE.Matrix4());
  const posRef  = useRef(new THREE.Vector3());
  const quatRef = useRef(new THREE.Quaternion());
  const sclRef  = useRef(new THREE.Vector3());
  const colRef  = useRef(new THREE.Color());

  useFrame(() => {
    const mesh = meshRef.current;
    if (!mesh) return;

    const decoded = getNodeFrame();
    if (!decoded) { mesh.count = 0; return; }
    const { portCount, portView, nodeCount, nodeView } = decoded;

    const q = quatRef.current;
    // Instance index MUST stay == buffer port row so a raycast hit's instanceId is the port
    // row Go resolves. Every row 0..portCount-1 is filled; a port whose owning node has not
    // yet streamed is hidden with a zero-scale (degenerate) matrix rather than skipped.
    const n = Math.min(portCount, capacity);
    for (let i = 0; i < n; i++) {
      const nodeRow = readPortNodeRow(portView, i);
      if (nodeRow < 0 || nodeRow >= nodeCount) {
        sclRef.current.setScalar(0); // hide until the owning node resolves
        posRef.current.set(0, 0, 0);
      } else {
        // Pointer hover (Go-owned Hovered column): pre-branch PortSphere isHov look — the
        // port sphere grows (scale 1.3) and turns #aaddff. Unhovered stays scale 1 + owner
        // stroke. (No port-selected concept in the buffer path, so selected-1.5 is n/a.)
        const hov = readPortHovered(portView, i) !== 0;
        sclRef.current.setScalar(hov ? PORT_HOVER_SCALE : 1);
        // World placement = Go's streamed authoritative port world position (PX/PY/PZ) —
        // the SAME point the connected edge's endpoint uses (portWorldPosAimed), so the
        // marker IS the edge endpoint by construction (no client-side recompute).
        posRef.current.set(
          readPortPX(portView, i),
          readPortPY(portView, i),
          readPortPZ(portView, i),
        );
        mesh.setColorAt(i, colRef.current.set(hov ? PORT_HOVER_COLOR : nodeRowColors(nodeView, nodeRow).stroke));
      }
      matRef.current.compose(posRef.current, q, sclRef.current);
      mesh.setMatrixAt(i, matRef.current);
    }
    mesh.count = n;
    mesh.instanceMatrix.needsUpdate = true;
    if (mesh.instanceColor) mesh.instanceColor.needsUpdate = true;
    mesh.computeBoundingSphere();
  });

  return (
    <instancedMesh ref={meshRef} args={[undefined, undefined, capacity]} userData={{ [BUFFER_PORT_TAG]: true }} frustumCulled={false}>
      <sphereGeometry args={[PORT_SPHERE_R, 8, 8]} />
      <meshStandardMaterial />
    </instancedMesh>
  );
}
