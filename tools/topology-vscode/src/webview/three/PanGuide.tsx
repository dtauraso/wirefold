// PanGuide.tsx — polar pan-construction overlay, built on the polar toolkit (polar.ts).
//
// All geometry is POLAR: the cursor is an angle pair (θ, φ) about the diagram's top axis
// (world Y, the frame pole); the disk, green line, and right triangle are produced from those
// angles via the toolkit. No cross products, no world-vector math here — so the cross-product
// degeneracy (the 180° flips) cannot be expressed. Cartesian appears only at the edges: the
// cursor's raycast (input) and toWorld (output for drawing).

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
import { makeFrame, toWorld, fromWorld, equatorDir, equatorPoint, type Polar } from "./polar";

const LINE_WIDTH = 3; // px (≈3× the old 1px lines)

export function PanGuide({ nodes }: { nodes: RFNode<NodeData>[] }) {
  useNodeGeometryStore((s) => s.geoms);
  const { camera, gl, size } = useThree();

  const { disk, tri, spoke0, interLine } = useMemo(() => {
    const mk = (hex: number, w = LINE_WIDTH) => {
      const geo = new LineGeometry();
      geo.setPositions([0, 0, 0, 0, 0, 0]);
      const mat = new LineMaterial({ color: hex, linewidth: w, transparent: true, opacity: 0.9, depthTest: false });
      const line = new Line2(geo, mat);
      line.raycast = () => null;
      line.frustumCulled = false;
      line.renderOrder = 999;
      return line;
    };
    return { disk: mk(0xffcc44), tri: mk(0x44ddff), spoke0: mk(0xff3333, 4), interLine: mk(0x66ff66, 4) };
  }, []);

  useFrame(() => {
    if (nodes.length < 1) return;
    const { x, y, inside } = useCursorStore.getState();
    if (!inside) { disk.visible = tri.visible = spoke0.visible = interLine.visible = false; return; }
    disk.visible = tri.visible = spoke0.visible = interLine.visible = true;

    const cs = computeContentSphere(nodes);
    // Polar frame: pole = the diagram's top axis (world Y), fixed. Anchored to the diagram.
    const frame = makeFrame(cs.center, cs.radius, new THREE.Vector3(0, 1, 0));
    const R = frame.radius;

    // Cursor → world point on the sphere (raycast, edge input) → polar (θ, φ).
    const rect = gl.domElement.getBoundingClientRect();
    const ndcX = ((x - rect.left) / rect.width) * 2 - 1;
    const ndcY = -(((y - rect.top) / rect.height) * 2 - 1);
    const ray = new THREE.Raycaster();
    ray.setFromCamera(new THREE.Vector2(ndcX, ndcY), camera);
    const hit = new THREE.Vector3();
    if (!ray.ray.intersectSphere(new THREE.Sphere(frame.center, R), hit)) {
      ray.ray.closestPointToPoint(frame.center, hit);
      hit.sub(frame.center).setLength(R).add(frame.center);
    }
    const q: Polar = fromWorld(frame, hit);

    // Everything below is polar (angles) → world only via the toolkit's converters.
    const P = toWorld(frame, q);                          // cursor point on the sphere
    const gDir = equatorDir(frame, q.theta);              // green-line direction (equator at azimuth θ)

    // Disk = the meridian great circle at azimuth θ (plane of pole + green-line dir). Defined
    // by the angle θ → no degeneracy.
    const dPts: number[] = [];
    const N = 96;
    for (let i = 0; i <= N; i++) {
      const t = (i / N) * Math.PI * 2;
      const p = frame.center.clone()
        .add(frame.pole.clone().multiplyScalar(Math.cos(t) * R))
        .add(gDir.clone().multiplyScalar(Math.sin(t) * R));
      dPts.push(p.x, p.y, p.z);
    }
    disk.geometry.setPositions(dPts);

    // Green line = the equator (horizontal torus) diameter at azimuth θ.
    const ga = equatorPoint(frame, q.theta, R);
    const gb = equatorPoint(frame, q.theta, -R);
    interLine.geometry.setPositions([ga.x, ga.y, ga.z, gb.x, gb.y, gb.z]);

    // Right triangle (cartesian↔polar trig): hypotenuse = radius C→P; right angle at G, the
    // foot on the GREEN line (horizontal projection = R·sin φ along θ); height G→P = R·cos φ
    // along the pole. G is on the green line by construction — always exactly 90° at G.
    const G = equatorPoint(frame, q.theta, R * Math.sin(q.phi));
    const C = frame.center;
    tri.geometry.setPositions([C.x, C.y, C.z, G.x, G.y, G.z, P.x, P.y, P.z, C.x, C.y, C.z]);

    // Red radius r = C → P.
    spoke0.geometry.setPositions([C.x, C.y, C.z, P.x, P.y, P.z]);

    for (const l of [disk, tri, spoke0, interLine]) (l.material as LineMaterial).resolution.set(size.width, size.height);
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
