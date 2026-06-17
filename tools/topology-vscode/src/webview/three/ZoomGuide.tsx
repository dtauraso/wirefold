// ZoomGuide.tsx — live polar-zoom construction overlay.
//
// Zoom triangle: right angle at the diagram center C; one leg = r (C → camera, the camera's
// radial distance: r = R on the sphere, < R inside); other leg = C → selected node. The
// HYPOTENUSE runs camera → node, and zoom travels along it (in toward the node, out toward
// the camera). Shown only when a node is selected. Plot-only, like PanGuide.

import React, { useMemo } from "react";
import * as THREE from "three";
import { useThree, useFrame } from "@react-three/fiber";
import { Line2 } from "three/examples/jsm/lines/Line2.js";
import { LineGeometry } from "three/examples/jsm/lines/LineGeometry.js";
import { LineMaterial } from "three/examples/jsm/lines/LineMaterial.js";
import type { RFNode, NodeData } from "../types";
import { computeContentSphere } from "./interaction-controls";
import { nodeWorldPos } from "./geometry-helpers";
import { useNodeGeometryStore } from "./node-geometry";

export function ZoomGuide({ nodes, selectedId }: { nodes: RFNode<NodeData>[]; selectedId: string | null }) {
  useNodeGeometryStore((s) => s.geoms);
  const camera = useThree((s) => s.camera);
  const size = useThree((s) => s.size);

  const { tri, hyp } = useMemo(() => {
    const mk = (hex: number, w: number) => {
      const geo = new LineGeometry();
      geo.setPositions([0, 0, 0, 0, 0, 0]);
      const mat = new LineMaterial({ color: hex, linewidth: w, transparent: true, opacity: 0.9, depthTest: false });
      const line = new Line2(geo, mat);
      line.raycast = () => null;
      line.frustumCulled = false;
      line.renderOrder = 999;
      line.visible = false;
      return line;
    };
    return { tri: mk(0x66ff99, 3), hyp: mk(0xff55cc, 5) };
  }, []);

  useFrame(() => {
    const node = selectedId ? nodes.find((n) => n.id === selectedId) : undefined;
    if (!node) { tri.visible = false; hyp.visible = false; return; }
    const cs = computeContentSphere(nodes);
    const C = cs.center;
    const camPos = camera.position;
    const N = nodeWorldPos(node);

    // Triangle C → camera (tip of r) → node → C; hypotenuse = camera → node (zoom travel line).
    tri.geometry.setPositions([C.x, C.y, C.z, camPos.x, camPos.y, camPos.z, N.x, N.y, N.z, C.x, C.y, C.z]);
    hyp.geometry.setPositions([camPos.x, camPos.y, camPos.z, N.x, N.y, N.z]);
    (tri.material as LineMaterial).resolution.set(size.width, size.height);
    (hyp.material as LineMaterial).resolution.set(size.width, size.height);
    tri.visible = true;
    hyp.visible = true;
  });

  return (
    <>
      <primitive object={tri} />
      <primitive object={hyp} />
    </>
  );
}
