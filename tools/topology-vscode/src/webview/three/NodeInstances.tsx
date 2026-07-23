// NodeInstances.tsx — solid node render matching GraphNode's look: a SOLID sphere per node
// (fill from NODE_DEFS[kind].fill) plus a border torus ring (stroke from NODE_DEFS[kind].stroke),
// plus the invisible pick-proxy ring used to author a `port ∈ torus` lock. Split out of
// buffer-scene.tsx: pure buffer→GPU render, no state authority.

import { useRef, useContext } from "react";
import { useFrame } from "@react-three/fiber";
import * as THREE from "three";
import { getNodeFrameOrFallback } from "./node-stream-blocks";
import { getViewBlocks } from "./view-blocks";
import { EnvTexContext } from "./scene-env";
import {
  SHADING_PARAM_NODE_TRANSMISSION,
  SHADING_PARAM_NODE_THICKNESS,
  SHADING_PARAM_NODE_ROUGHNESS,
  SHADING_PARAM_NODE_IOR,
  SHADING_PARAM_NODE_METALNESS,
  SHADING_PARAM_NODE_CLEARCOAT,
  SHADING_PARAM_NODE_CLEARCOAT_ROUGHNESS,
  SHADING_PARAM_NODE_ENV_MAP_INTENSITY,
  SHADING_PARAM_NODE_OPACITY,
  SHADING_PARAM_RING_ROUGHNESS,
} from "../../schema/shading-params";
import {
  readNodeCX, readNodeCY, readNodeCZ, readNodeRadius,
  readOverlaySelSpherePoles,
} from "../../schema/buffer-layout";
import {
  BUFFER_NODE_TAG, BUFFER_RING_TAG, NODE_SPHERE_RADIUS,
  NODE_RING_TUBE_RATIO, RING_PICK_TUBE_RATIO, nodeRowColors,
} from "./buffer-scene-shared";

export function NodeInstances({ capacity }: { capacity: number }) {
  const envTex = useContext(EnvTexContext);
  const bodyRef = useRef<THREE.InstancedMesh>(null);
  const ringRef = useRef<THREE.InstancedMesh>(null);
  const ringPickRef = useRef<THREE.InstancedMesh>(null);
  const matRef  = useRef(new THREE.Matrix4());
  const posRef  = useRef(new THREE.Vector3());
  const quatRef = useRef(new THREE.Quaternion());
  const sclRef  = useRef(new THREE.Vector3());
  const colRef  = useRef(new THREE.Color());

  useFrame(() => {
    const body = bodyRef.current;
    const ring = ringRef.current;
    const ringPick = ringPickRef.current;
    if (!body || !ring || !ringPick) return;

    const blocks = getViewBlocks();
    const decodedNode = getNodeFrameOrFallback();
    if (!decodedNode || !blocks) { body.count = 0; ring.count = 0; ringPick.count = 0; return; }
    const { overlayView } = blocks;
    const { nodeCount, nodeView } = decodedNode;
    // The invisible ring pick-proxy (RING_PICK_TUBE_RATIO) is a pick target ONLY while the
    // selSpherePoles ("select") overlay is on — that's the one mode where a torus click
    // authors a `port ∈ torus` equation. When the overlay is off, count=0 removes it from
    // the raycast scene entirely, so it never steals hits from the node body (BUFFER_NODE_TAG)
    // underneath it. The VISIBLE ring (ringRef, NODE_RING_TUBE_RATIO) is unaffected — it stays
    // rendered in both modes; only the fat invisible pick torus is gated.
    const selectModeOn = readOverlaySelSpherePoles(overlayView) !== 0;

    const n = Math.min(nodeCount, capacity);
    const q = quatRef.current; // identity (no per-node rotation)
    for (let i = 0; i < n; i++) {
      const r = readNodeRadius(nodeView, i) || NODE_SPHERE_RADIUS;
      posRef.current.set(
        readNodeCX(nodeView, i),
        readNodeCY(nodeView, i),
        readNodeCZ(nodeView, i),
      );
      // Body: unit sphere scaled to the node radius.
      sclRef.current.setScalar(r);
      matRef.current.compose(posRef.current, q, sclRef.current);
      body.setMatrixAt(i, matRef.current);
      // Ring: unit torus (major radius 1) scaled to the node radius; tube thickness
      // is baked into the geometry as a fraction of that radius (NODE_RING_TUBE_RATIO).
      ring.setMatrixAt(i, matRef.current);
      // Invisible pick-proxy: identical transform to the visible ring, just a thicker
      // raycast target (see RING_PICK_TUBE_RATIO comment).
      ringPick.setMatrixAt(i, matRef.current);

      const { fill, stroke } = nodeRowColors(nodeView, i);
      body.setColorAt(i, colRef.current.set(fill));
      ring.setColorAt(i, colRef.current.set(stroke));
    }
    body.count = n;
    ring.count = n;
    ringPick.count = selectModeOn ? n : 0;
    body.instanceMatrix.needsUpdate = true;
    ring.instanceMatrix.needsUpdate = true;
    ringPick.instanceMatrix.needsUpdate = true;
    if (body.instanceColor) body.instanceColor.needsUpdate = true;
    if (ring.instanceColor) ring.instanceColor.needsUpdate = true;
    // Refresh the InstancedMesh bounding sphere so raycast picking stays accurate as
    // nodes move (three.js early-outs a ray against a cached union sphere; a dragged
    // node outside the stale sphere would otherwise be un-pickable). Cheap for the
    // small node counts here.
    body.computeBoundingSphere();
    if (selectModeOn) ringPick.computeBoundingSphere();
  });

  return (
    <>
      <instancedMesh ref={bodyRef} args={[undefined, undefined, capacity]} userData={{ [BUFFER_NODE_TAG]: true }} frustumCulled={false}>
        <sphereGeometry args={[1, 16, 16]} />
        {/* Match GraphNode's glassy translucent body EXACTLY (scene-graph.tsx): a
            meshPhysicalMaterial with transmission + depthWrite=false + opacity 0.92 so
            the node interior (held/interior beads) shows through. Per-node fill is the
            instanceColor (setColorAt below); the shared material color stays white so
            instanceColor is applied verbatim. envMap comes from the same PMREM context
            the JSON path uses (BufferScene is wrapped in ProceduralEnvProvider). */}
        <meshPhysicalMaterial
          transmission={SHADING_PARAM_NODE_TRANSMISSION}
          thickness={SHADING_PARAM_NODE_THICKNESS}
          roughness={SHADING_PARAM_NODE_ROUGHNESS}
          ior={SHADING_PARAM_NODE_IOR}
          metalness={SHADING_PARAM_NODE_METALNESS}
          clearcoat={SHADING_PARAM_NODE_CLEARCOAT}
          clearcoatRoughness={SHADING_PARAM_NODE_CLEARCOAT_ROUGHNESS}
          envMap={envTex ?? undefined}
          envMapIntensity={SHADING_PARAM_NODE_ENV_MAP_INTENSITY}
          transparent
          opacity={SHADING_PARAM_NODE_OPACITY}
          depthWrite={false}
        />
      </instancedMesh>
      <instancedMesh ref={ringRef} args={[undefined, undefined, capacity]} userData={{ [BUFFER_RING_TAG]: true }} frustumCulled={false}>
        <torusGeometry args={[1, NODE_RING_TUBE_RATIO, 8, 32]} />
        <meshStandardMaterial roughness={SHADING_PARAM_RING_ROUGHNESS} metalness={0} depthWrite={false} />
      </instancedMesh>
      {/* Invisible pick-proxy torus: same per-instance transform as the visible ring above,
          but a much thicker tube (RING_PICK_TUBE_RATIO) so the ring band is a generous
          raycast target. colorWrite/depthWrite false + zero opacity means it draws nothing;
          it must stay visible (not visible={false}) or three.js Raycaster skips it entirely.
          Tagged BUFFER_RING_TAG so pickBufferRing (scene-content.tsx) resolves its instanceId
          to the same node row as the visible ring. */}
      <instancedMesh ref={ringPickRef} args={[undefined, undefined, capacity]} userData={{ [BUFFER_RING_TAG]: true }} frustumCulled={false}>
        <torusGeometry args={[1, RING_PICK_TUBE_RATIO, 8, 32]} />
        <meshBasicMaterial transparent opacity={0} colorWrite={false} depthWrite={false} />
      </instancedMesh>
    </>
  );
}
