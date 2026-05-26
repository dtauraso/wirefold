import { NODE_TYPES, type Node as SpecNode } from "../../../schema";
import type { Fold, NodeView } from "../../state/viewer/types";

export const COLLAPSED_FOLD_W = 140;
export const COLLAPSED_FOLD_H = 60;
export const EXPANDED_PADDING = 16;

export function expandedBounds(
  fold: Fold,
  byId: Map<string, SpecNode>,
  nodeViews: Record<string, NodeView> = {},
) {
  let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
  for (const id of fold.memberIds) {
    const n = byId.get(id);
    if (!n) continue;
    const def = NODE_TYPES[n.type];
    const w = def?.width ?? 110;
    const h = def?.height ?? 60;
    const nv = nodeViews[id];
    const nx = nv?.x ?? 0;
    const ny = nv?.y ?? 0;
    if (nx < minX) minX = nx;
    if (ny < minY) minY = ny;
    if (nx + w > maxX) maxX = nx + w;
    if (ny + h > maxY) maxY = ny + h;
  }
  if (!Number.isFinite(minX)) {
    return { x: fold.position[0], y: fold.position[1], w: COLLAPSED_FOLD_W, h: COLLAPSED_FOLD_H };
  }
  return {
    x: minX - EXPANDED_PADDING,
    y: minY - EXPANDED_PADDING,
    w: (maxX - minX) + EXPANDED_PADDING * 2,
    h: (maxY - minY) + EXPANDED_PADDING * 2,
  };
}
