// PanGuide.tsx — live polar-pan construction overlay.
//
// Shows, for the current cursor, the two things that define polar pan:
//   • the DISK the radius sweeps — its plane follows the cursor's motion (spanned by the
//     radius and the motion direction), and
//   • the right TRIANGLE on that disk: hypotenuse = the radius (center → cursor point),
//     base = the radius projected onto the disk's horizontal axis = the pan offset.
//
// Drawn with fat lines (Line2, pixel width) so the outlines are easy to see. Plot-only:
// reads the cursor (cursor-store) + camera, derives geometry each frame.

import React, { useMemo, useRef } from "react";
import * as THREE from "three";
import { useThree, useFrame } from "@react-three/fiber";
import { Line2 } from "three/examples/jsm/lines/Line2.js";
import { LineGeometry } from "three/examples/jsm/lines/LineGeometry.js";
import { LineMaterial } from "three/examples/jsm/lines/LineMaterial.js";
import type { RFNode, NodeData } from "../types";
import { computeContentSphere } from "./interaction-controls";
import { useCursorStore } from "./cursor-store";
import { useNodeGeometryStore } from "./node-geometry";

const LINE_WIDTH = 3; // px (≈3× the old 1px lines)

export function PanGuide({ nodes }: { nodes: RFNode<NodeData>[] }) {
  // Re-derive when Go streams geometry (sphere center/radius move with the diagram).
  useNodeGeometryStore((s) => s.geoms);
  const { camera, gl, size } = useThree();
  const prevP = useRef<THREE.Vector3 | null>(null);  // last cursor point on the sphere
  const tanDir = useRef(new THREE.Vector3(1, 0, 0)); // smoothed drag direction (tangent), stable

  // Fat-line objects (Line2) for the disk, triangle, and disk spokes.
  const { disk, tri, spoke0 } = useMemo(() => {
    const mk = (hex: number, w = LINE_WIDTH) => {
      const geo = new LineGeometry();
      geo.setPositions([0, 0, 0, 0, 0, 0]); // seed so attributes exist before the first frame
      const mat = new LineMaterial({ color: hex, linewidth: w, transparent: true, opacity: 0.9, depthTest: false });
      const line = new Line2(geo, mat);
      line.raycast = () => null;
      line.frustumCulled = false;
      line.renderOrder = 999;
      return line;
    };
    // spoke0 = the radius to the cursor (the radius r), highlighted red.
    return { disk: mk(0xffcc44), tri: mk(0x44ddff), spoke0: mk(0xff3333, 4) };
  }, []);

  useFrame(() => {
    if (nodes.length < 1) return;
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
    const rHat = radius.clone().normalize();

    // The disk TRACKS THE DRAG DIRECTION but stays stable: keep a SMOOTHED tangent direction
    // (tanDir) instead of the raw per-frame cross product. Smoothing averages out the noise
    // that made the raw normal flip 180° on a near-straight drag; a genuine reversal still
    // turns it around. When motion is near-radial (no tangential part) tanDir is just held.
    if (prevP.current) {
      const m = P.clone().sub(prevP.current);
      const t = m.clone().sub(rHat.clone().multiplyScalar(m.dot(rHat))); // tangential part of motion
      if (t.lengthSq() > 1e-7) {
        t.normalize();
        tanDir.current.lerp(t, 0.25);
        if (tanDir.current.lengthSq() < 1e-6) tanDir.current.copy(t);
        tanDir.current.normalize();
      }
    }
    prevP.current = P.clone();

    // Disk = great circle in the plane the radius sweeps = span(rHat, tanDir): e1 = rHat,
    // e2 = tanDir orthogonalized against rHat.
    const e1 = rHat.clone();
    let e2 = tanDir.current.clone().sub(e1.clone().multiplyScalar(tanDir.current.dot(e1)));
    if (e2.lengthSq() < 1e-8) e2 = Math.abs(e1.y) < 0.9 ? new THREE.Vector3(0, 1, 0) : new THREE.Vector3(1, 0, 0);
    e2.sub(e1.clone().multiplyScalar(e2.dot(e1))).normalize();
    const dPts: number[] = [];
    const N = 96;
    for (let i = 0; i <= N; i++) {
      const ang = (i / N) * Math.PI * 2;
      const p = C.clone()
        .add(e1.clone().multiplyScalar(Math.cos(ang) * R))
        .add(e2.clone().multiplyScalar(Math.sin(ang) * R));
      dPts.push(p.x, p.y, p.z);
    }
    disk.geometry.setPositions(dPts);

    // Right triangle ON the disk: hypotenuse = radius (C→P, in the disk plane); base along the
    // disk's HORIZONTAL axis (disk plane ∩ world-horizontal = n × pole), forced toward the
    // cursor so it never flips; height = the remaining in-plane leg. Right angle at Q.
    const n = new THREE.Vector3().crossVectors(e1, e2); // disk normal (stable, from smoothed plane)
    let hAxis = new THREE.Vector3().crossVectors(n, pole);
    if (hAxis.lengthSq() < 1e-8) hAxis = e1.clone(); // disk near-horizontal → degenerate
    hAxis.normalize();
    if (radius.dot(hAxis) < 0) hAxis.negate();        // base points toward the cursor
    const Q = C.clone().add(hAxis.multiplyScalar(radius.dot(hAxis)));
    tri.geometry.setPositions([C.x, C.y, C.z, Q.x, Q.y, Q.z, P.x, P.y, P.z, C.x, C.y, C.z]);

    // The radius r to the cursor (C → P).
    spoke0.geometry.setPositions([C.x, C.y, C.z, P.x, P.y, P.z]);
    (spoke0.material as LineMaterial).resolution.set(size.width, size.height);

    // Fat lines need the viewport resolution to size their pixel width.
    (disk.material as LineMaterial).resolution.set(size.width, size.height);
    (tri.material as LineMaterial).resolution.set(size.width, size.height);
  });

  if (nodes.length < 1) return null;
  return (
    <>
      <primitive object={disk} />
      <primitive object={tri} />
      <primitive object={spoke0} />
    </>
  );
}
