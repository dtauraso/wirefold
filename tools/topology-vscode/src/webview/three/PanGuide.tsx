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
import { postLog } from "../log/post";

const LINE_WIDTH = 3; // px (≈3× the old 1px lines)

export function PanGuide({ nodes }: { nodes: RFNode<NodeData>[] }) {
  // Re-derive when Go streams geometry (sphere center/radius move with the diagram).
  useNodeGeometryStore((s) => s.geoms);
  const { camera, gl, size } = useThree();
  // The disk plane + triangle are FROZEN once (they "stay put"); only the red radius spins.
  const frozen = useRef<{ e1: THREE.Vector3; e2: THREE.Vector3; Pf: THREE.Vector3 } | null>(null);

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
    const pole = new THREE.Vector3(0, 1, 0); // diagram top axis

    // Cursor → point P on the diagram sphere (raycast; clamp to the silhouette on miss).
    const { x, y, inside } = useCursorStore.getState();
    if (!frozen.current && !inside) { disk.visible = false; tri.visible = false; spoke0.visible = false; interLine.visible = false; return; }
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

    // FREEZE the disk plane + triangle once, so they "stay put". Default plane = the meridian
    // through the first cursor (radius + pole); the triangle is the right triangle at that
    // first cursor point. Held thereafter — they no longer chase the cursor.
    if (!frozen.current) {
      const e1 = rHat.clone();
      let e2 = pole.clone().sub(e1.clone().multiplyScalar(pole.dot(e1)));
      if (e2.lengthSq() < 1e-6) e2 = new THREE.Vector3(1, 0, 0).sub(e1.clone().multiplyScalar(e1.x));
      e2.normalize();
      frozen.current = { e1, e2, Pf: P.clone() };
      postLog("panguide-freeze", {
        cursor: [Math.round(x), Math.round(y)], // if this is (0,0)/stale, it froze before you moved
        Pf: [+P.x.toFixed(1), +P.y.toFixed(1), +P.z.toFixed(1)],
        e1: [+e1.x.toFixed(2), +e1.y.toFixed(2), +e1.z.toFixed(2)],
      });
    }
    const { e1, e2, Pf } = frozen.current;
    const n = new THREE.Vector3().crossVectors(e1, e2); // frozen disk normal

    // Disk = great circle in the FROZEN plane.
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

    // Triangle STAYS PUT: built from the frozen cursor point Pf. hypotenuse = C→Pf; base along
    // the disk's horizontal axis (n × pole) toward Pf; height the remaining in-plane leg.
    const radiusF = Pf.clone().sub(C);
    let hAxis = new THREE.Vector3().crossVectors(n, pole);
    if (hAxis.lengthSq() < 1e-8) hAxis = e1.clone();
    hAxis.normalize();
    if (radiusF.dot(hAxis) < 0) hAxis.negate();
    const Q = C.clone().add(hAxis.multiplyScalar(radiusF.dot(hAxis)));
    tri.geometry.setPositions([C.x, C.y, C.z, Q.x, Q.y, Q.z, Pf.x, Pf.y, Pf.z, C.x, C.y, C.z]);

    // The red radius SPINS OPPOSITE to the cursor's angular motion (instead of pointing at the
    // cursor): the cursor's angle in the frozen plane is a = atan2(radius·e2, radius·e1); the
    // marker sits at −a, so as the cursor turns one way the marker turns the other.
    const a = Math.atan2(radius.dot(e2), radius.dot(e1));
    const sd = e1.clone().multiplyScalar(Math.cos(-a)).add(e2.clone().multiplyScalar(Math.sin(-a)));
    const sp = C.clone().add(sd.multiplyScalar(R));
    spoke0.geometry.setPositions([C.x, C.y, C.z, sp.x, sp.y, sp.z]);
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
