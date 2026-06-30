// scene-occlusion.ts — computeOcclusionCounts: screen-space overlap detection.
import * as THREE from "three";
import type { RFNode, NodeData } from "../types";
import { nodeWorldPos, nodeRadius, ndcToPixel } from "./geometry-helpers";

// ---------------------------------------------------------------------------
// computeOcclusionCounts: given projected positions + world positions, return
// a map from frontNodeId → count of nodes hidden directly behind it.
//
// Two nodes "overlap" if their projected screen-center distance < front node's
// projected radius. Front = nearer to camera. N = count of farther overlapping
// nodes behind the front one.
// ---------------------------------------------------------------------------

export function computeOcclusionCounts(
  nodes: RFNode<NodeData>[],
  camera: THREE.Camera,
  size: { w: number; h: number },
): Map<string, number> {
  if (nodes.length < 2) return new Map();

  const wh = { width: size.w, height: size.h };

  // Project each node: screen px/py, camera distance, screen radius.
  type Proj = { id: string; px: number; py: number; dist: number; screenR: number };
  const projected: Proj[] = nodes.map((n) => {
    const worldCenter = nodeWorldPos(n);
    const dist = worldCenter.distanceTo(camera.position);

    const center = worldCenter.clone().project(camera);
    const { px, py } = ndcToPixel(center.x, center.y, wh);

    // Compute screen radius: project a point offset by the world node radius.
    const worldR = nodeRadius(n);
    const offsetWorld = worldCenter.clone().add(new THREE.Vector3(worldR, 0, 0));
    const offsetProj = offsetWorld.clone().project(camera);
    const offsetPx = ndcToPixel(offsetProj.x, offsetProj.y, wh).px;
    const screenR = Math.abs(offsetPx - px);

    return { id: n.id, px, py, dist, screenR };
  });

  const counts = new Map<string, number>();

  for (let i = 0; i < projected.length; i++) {
    for (let j = i + 1; j < projected.length; j++) {
      const a = projected[i];
      const b = projected[j];
      const screenDist = Math.hypot(a.px - b.px, a.py - b.py);

      // Overlap: use front node's screen radius as threshold.
      const front = a.dist < b.dist ? a : b;

      if (screenDist < front.screenR) {
        counts.set(front.id, (counts.get(front.id) ?? 0) + 1);
      }
    }
  }

  return counts;
}
