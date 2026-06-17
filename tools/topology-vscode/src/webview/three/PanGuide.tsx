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
  const prevP = useRef<THREE.Vector3 | null>(null);   // last cursor point on the sphere
  const lastNormal = useRef(new THREE.Vector3(0, 0, 1)); // last disk normal (held when not moving)

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

    const radius = P.clone().sub(C); // hypotenuse, length R

    // The DISK FOLLOWS THE MOUSE MOTION: its plane is the one the radius sweeps as the cursor
    // moves = spanned by the radius and the cursor's motion direction. Normal = radius × motion
    // (the rotation axis). When the cursor isn't moving, hold the last orientation.
    let n = lastNormal.current.clone();
    if (prevP.current) {
      const motion = P.clone().sub(prevP.current);
      if (motion.lengthSq() > 1e-10) {
        const cand = new THREE.Vector3().crossVectors(radius, motion);
        if (cand.lengthSq() > 1e-10) { n = cand.normalize(); lastNormal.current.copy(n); }
      }
    }
    prevP.current = P.clone();

    // In-plane basis: e1 along the radius, e2 = n × e1 (the other in-plane axis).
    const e1 = radius.clone().normalize();
    const e2 = new THREE.Vector3().crossVectors(n, e1).normalize();
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

    // Right triangle ON the disk: hypotenuse = radius (C→P); base along the disk's HORIZONTAL
    // axis (where the disk plane meets the world-horizontal plane = n × pole); height drops
    // from P perpendicular to that axis. base = the pan offset.
    let hAxis = new THREE.Vector3().crossVectors(n, pole);
    if (hAxis.lengthSq() < 1e-9) hAxis = e1.clone(); // disk lies flat → degenerate
    hAxis.normalize();
    const Q = C.clone().add(hAxis.clone().multiplyScalar(radius.dot(hAxis))); // foot on the horizontal axis
    triRef.current.setFromPoints([C.clone(), Q.clone(), P.clone()]);
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
