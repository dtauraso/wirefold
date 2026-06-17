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

import React, { useMemo } from "react";
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

  // Fat-line objects (Line2) for the disk and triangle.
  const { disk, tri } = useMemo(() => {
    const mk = (hex: number) => {
      const geo = new LineGeometry();
      geo.setPositions([0, 0, 0, 0, 0, 0]); // seed so attributes exist before the first frame
      const mat = new LineMaterial({ color: hex, linewidth: LINE_WIDTH, transparent: true, opacity: 0.9, depthTest: false });
      const line = new Line2(geo, mat);
      line.raycast = () => null;
      line.frustumCulled = false;
      line.renderOrder = 999;
      return line;
    };
    return { disk: mk(0xffcc44), tri: mk(0x44ddff) };
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

    // POSITION-BASED disk (no motion, no flipping). The disk is the MERIDIAN great circle
    // through the cursor: the plane of {horizontal-toward-cursor, pole}. It reorients toward
    // wherever the cursor is (its azimuth follows the cursor). Because it's derived from the
    // cursor POSITION, never from a motion direction, there's no normal whose sign can flip
    // 180° — the cause of the jitter.
    const v = pole.clone().multiplyScalar(radius.dot(pole)); // vertical leg (along the pole)
    const h = radius.clone().sub(v);                          // horizontal leg, toward the cursor
    let e1 = h.clone();
    if (e1.lengthSq() < 1e-9) e1 = new THREE.Vector3(1, 0, 0); // cursor over a pole
    e1.normalize();
    const e2 = pole.clone();
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

    // Right triangle on that disk: hypotenuse = radius (C→P); base = h (C→Q, horizontal,
    // pointing TOWARD the cursor); height = v (Q→P, along the pole). Right angle at Q.
    const Q = C.clone().add(h);
    tri.geometry.setPositions([C.x, C.y, C.z, Q.x, Q.y, Q.z, P.x, P.y, P.z, C.x, C.y, C.z]);

    // Fat lines need the viewport resolution to size their pixel width.
    (disk.material as LineMaterial).resolution.set(size.width, size.height);
    (tri.material as LineMaterial).resolution.set(size.width, size.height);
  });

  if (nodes.length < 1) return null;
  return (
    <>
      <primitive object={disk} />
      <primitive object={tri} />
    </>
  );
}
