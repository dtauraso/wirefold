// PanGuide.tsx — live polar-pan construction overlay.
//
// Shows, for the current cursor, the two things that define polar pan:
//   • the DISK the radius sweeps (the latitude circle on the diagram sphere at the
//     cursor's colatitude about the diagram's top pole), and
//   • the circumscribed RIGHT TRIANGLE: hypotenuse = the radius (center → cursor
//     point on the sphere), base = that radius PROJECTED onto the horizontal. The
//     base is the pan offset.
//
// Plot-only: reads the cursor (cursor-store) + camera, derives geometry each frame.
// No navigation logic here — this just draws what polar pan will use.

import React, { useRef } from "react";
import * as THREE from "three";
import { useThree, useFrame } from "@react-three/fiber";
import type { RFNode, NodeData } from "../types";
import { computeContentSphere } from "./interaction-controls";
import { useCursorStore } from "./cursor-store";
import { useNodeGeometryStore } from "./node-geometry";

export function PanGuide({ nodes }: { nodes: RFNode<NodeData>[] }) {
  // Re-derive when Go streams geometry (sphere center/radius move with the diagram).
  useNodeGeometryStore((s) => s.geoms);
  const { camera, gl } = useThree();
  const diskRef = useRef<THREE.BufferGeometry>(null);
  const triRef = useRef<THREE.BufferGeometry>(null);

  useFrame(() => {
    if (nodes.length < 1 || !diskRef.current || !triRef.current) return;
    const cs = computeContentSphere(nodes);
    const C = cs.center;
    const R = cs.radius;
    const pole = new THREE.Vector3(0, 1, 0); // diagram top axis

    // Cursor → point P on the diagram sphere (raycast; clamp to the silhouette on miss).
    const { x, y } = useCursorStore.getState();
    const rect = gl.domElement.getBoundingClientRect();
    const ndcX = ((x - rect.left) / rect.width) * 2 - 1;
    const ndcY = -(((y - rect.top) / rect.height) * 2 - 1);
    const ray = new THREE.Raycaster();
    ray.setFromCamera(new THREE.Vector2(ndcX, ndcY), camera);
    const P = new THREE.Vector3();
    if (!ray.ray.intersectSphere(new THREE.Sphere(C, R), P)) {
      ray.ray.closestPointToPoint(C, P);
      P.sub(C).setLength(R).add(C);
    }

    // Radius (hypotenuse) and its split into vertical (along pole) + horizontal (base).
    const radius = P.clone().sub(C);
    const v = pole.clone().multiplyScalar(radius.dot(pole)); // vertical leg
    const h = radius.clone().sub(v);                          // horizontal leg = base (pan offset)
    const Q = C.clone().add(h);                               // right-angle vertex

    // Triangle outline (lineLoop auto-closes P→C): C → Q (base, horizontal) → P (height,
    // vertical). Right angle at Q (base ⊥ height); hypotenuse C→P = the radius.
    triRef.current.setFromPoints([C.clone(), Q.clone(), P.clone()]);

    // Disk outline: the GREAT CIRCLE the radius sweeps — center C, radius R, in the plane
    // spanned by {horizontal-toward-mouse, pole}. Vertical, tilted toward the cursor (below
    // center on the cursor side, above on the far side); the triangle lies on it.
    let e1 = h.clone();
    if (e1.lengthSq() < 1e-9) e1 = new THREE.Vector3(1, 0, 0); // cursor over a pole
    e1.normalize();                       // horizontal, toward the cursor's azimuth
    const e2 = pole.clone();              // vertical (the pole)
    const pts: THREE.Vector3[] = [];
    const N = 96;
    for (let i = 0; i <= N; i++) {
      const ang = (i / N) * Math.PI * 2;
      pts.push(
        C.clone()
          .add(e1.clone().multiplyScalar(Math.cos(ang) * R))
          .add(e2.clone().multiplyScalar(Math.sin(ang) * R)),
      );
    }
    diskRef.current.setFromPoints(pts);
  });

  if (nodes.length < 1) return null;
  return (
    <>
      <lineLoop raycast={() => null}>
        <bufferGeometry ref={diskRef} />
        <lineBasicMaterial color="#ffcc44" transparent opacity={0.85} depthTest={false} />
      </lineLoop>
      <lineLoop raycast={() => null}>
        <bufferGeometry ref={triRef} />
        <lineBasicMaterial color="#44ddff" transparent opacity={0.9} depthTest={false} />
      </lineLoop>
    </>
  );
}
