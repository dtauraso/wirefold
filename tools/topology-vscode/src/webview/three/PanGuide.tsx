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
  // Disk orientation comes from TWO points (previous + current cursor on the sphere), sampled
  // over a small screen baseline so it isn't per-frame noise.
  const anchorP = useRef<THREE.Vector3 | null>(null);          // previous cursor sphere point
  const anchorXY = useRef<{ x: number; y: number } | null>(null);
  const lastN = useRef(new THREE.Vector3(0, 0, 1));            // current disk normal (sign irrelevant)

  // Fat-line objects (Line2) for the disk, triangle, and disk spokes.
  const { disk, tri, spoke0, interLine } = useMemo(() => {
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
    // interLine = where the HORIZONTAL torus (equator) meets the mouse-following disk.
    return { disk: mk(0xffcc44), tri: mk(0x44ddff), spoke0: mk(0xff3333, 4), interLine: mk(0x66ff66, 4) };
  }, []);

  useFrame(() => {
    if (nodes.length < 1) return;
    const cs = computeContentSphere(nodes);
    const C = cs.center;
    const R = cs.radius;
    // Use the VIEW-UP (the view-aligned horizontal torus's normal = camera up), not world Y,
    // so the triangle's base/right-angle and the equator-intersection line land on the actual
    // (view-aligned) horizontal torus where it crosses the disk.
    const pole = new THREE.Vector3(0, 1, 0).applyQuaternion(camera.quaternion).normalize();

    // Cursor → point P on the diagram sphere (raycast; clamp to the silhouette on miss).
    const { x, y, inside } = useCursorStore.getState();
    if (!inside) { disk.visible = false; tri.visible = false; spoke0.visible = false; interLine.visible = false; return; }
    disk.visible = true; tri.visible = true; spoke0.visible = true;
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

    // Disk orientation from TWO points. normal n = anchorRadius × curRadius (the plane through
    // both radii). Updated when the cursor has moved a small baseline; a stationary cursor (or
    // first frame) gets the DEFAULT disk = the meridian (radius + pole). Nothing drawn depends
    // on n's SIGN, so reversing direction (n → −n, same plane) never flips the disk 180°.
    let n = lastN.current.clone();
    const movedPx = anchorXY.current ? Math.hypot(x - anchorXY.current.x, y - anchorXY.current.y) : 0;
    if (!anchorP.current) {
      let dn = new THREE.Vector3().crossVectors(rHat, pole); // default meridian normal
      if (dn.lengthSq() < 1e-8) dn = new THREE.Vector3(0, 0, 1);
      n = dn.normalize();
      lastN.current.copy(n);
      anchorP.current = P.clone();
      anchorXY.current = { x, y };
    } else if (movedPx > 5) {
      const cand = new THREE.Vector3().crossVectors(anchorP.current.clone().sub(C), radius);
      if (cand.lengthSq() > 1e-10) { n = cand.normalize(); lastN.current.copy(n); }
      anchorP.current = P.clone();
      anchorXY.current = { x, y };
    }

    // Disk = great circle in that plane: e1 = rHat (in plane), e2 = n × e1.
    const e1 = rHat.clone();
    let e2 = new THREE.Vector3().crossVectors(n, e1);
    if (e2.lengthSq() < 1e-8) e2 = new THREE.Vector3().crossVectors(n, new THREE.Vector3(1, 0, 0));
    e2.normalize();
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

    // Triangle on the disk: hypotenuse = radius (C→P); base along the disk's horizontal axis
    // (n × pole). Unlike the disk, the base KEEPS n's sign — so when the mouse changes
    // direction (n → −n) the TRIANGLE flips to the other side, while the disk does not.
    let hAxis = new THREE.Vector3().crossVectors(n, pole);
    if (hAxis.lengthSq() < 1e-8) hAxis = e1.clone();
    hAxis.normalize();
    const Q = C.clone().add(hAxis.multiplyScalar(radius.dot(hAxis)));
    tri.geometry.setPositions([C.x, C.y, C.z, Q.x, Q.y, Q.z, P.x, P.y, P.z, C.x, C.y, C.z]);

    // The radius r to the cursor (C → P), no spin.
    spoke0.geometry.setPositions([C.x, C.y, C.z, P.x, P.y, P.z]);
    (spoke0.material as LineMaterial).resolution.set(size.width, size.height);

    // Intersection of the HORIZONTAL torus (equator, plane normal = pole) with the disk
    // (plane normal = n): the diameter through their two crossing points, along pole × n.
    const interDir = new THREE.Vector3().crossVectors(pole, n);
    if (interDir.lengthSq() > 1e-8) {
      interDir.normalize();
      const ia = C.clone().add(interDir.clone().multiplyScalar(R));
      const ib = C.clone().add(interDir.clone().multiplyScalar(-R));
      interLine.geometry.setPositions([ia.x, ia.y, ia.z, ib.x, ib.y, ib.z]);
      interLine.visible = true;
    } else {
      interLine.visible = false; // disk is horizontal → coincident with the equator
    }
    (interLine.material as LineMaterial).resolution.set(size.width, size.height);

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
      <primitive object={interLine} />
    </>
  );
}
