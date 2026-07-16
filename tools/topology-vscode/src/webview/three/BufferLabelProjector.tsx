// BufferLabelProjector.tsx — buffer-driven node label projector: each ~2 frames it reads the
// snapshot's node block, projects each node's top (center.y+radius) and center to screen,
// and reports {row,label,px,py,cx,cy} — the row is the node's buffer node-row index
// (identity) and the label is decoded straight from the buffer's label section (nodeLabel).
// Split out of buffer-scene.tsx. Mirrors the old JSON-path LabelProjector but sourced
// entirely from the buffer, no id table. The DOM label pills (ThreeView) render from these
// positions. Pure projection — no store writes.

import { useRef } from "react";
import { useFrame, useThree } from "@react-three/fiber";
import * as THREE from "three";
import { getLatestSnapshot } from "../snapshot-buffer";
import { decodeSnapshot, nodeLabel } from "./buffer-decode";
import { ndcToPixel } from "./geometry-helpers";
import { readNodeCX, readNodeCY, readNodeCZ, readNodeRadius } from "../../schema/buffer-layout";
import type { BufferLabelPos } from "./buffer-scene-shared";

const _bufTopScratch = new THREE.Vector3();
const _bufCenterScratch = new THREE.Vector3();

export function BufferLabelProjector({ onPositions }: {
  onPositions: (positions: BufferLabelPos[]) => void;
}) {
  const { camera, size } = useThree();
  const frameCountRef = useRef(0);

  useFrame(() => {
    frameCountRef.current++;
    if (frameCountRef.current % 2 !== 0) return; // ~30fps, matches LabelProjector
    const snap = getLatestSnapshot();
    if (!snap) return;
    const decoded = decodeSnapshot(snap);
    if (!decoded) return;
    const { nodeCount, nodeView } = decoded;
    const positions: BufferLabelPos[] = [];
    for (let i = 0; i < nodeCount; i++) {
      const cx = readNodeCX(nodeView, i);
      const cy = readNodeCY(nodeView, i);
      const cz = readNodeCZ(nodeView, i);
      const r = readNodeRadius(nodeView, i);
      _bufTopScratch.set(cx, cy + r, cz).project(camera);
      const topPx = ndcToPixel(_bufTopScratch.x, _bufTopScratch.y, size);
      _bufCenterScratch.set(cx, cy, cz).project(camera);
      const centerPx = ndcToPixel(_bufCenterScratch.x, _bufCenterScratch.y, size);
      positions.push({ row: i, label: nodeLabel(decoded, i), px: topPx.px, py: topPx.py, cx: centerPx.px, cy: centerPx.py });
    }
    onPositions(positions);
  });

  return null;
}
