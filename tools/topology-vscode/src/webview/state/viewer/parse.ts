// Parser helpers for parseViewerState (viewerState.ts owns types + public API).

import type { StateValue } from "../../../schema";
import type {
  Camera, EdgeView, Fold, LegacyCameraBox,
  NodeView, SavedView,
} from "./types";

export const isObj = (v: unknown): v is Record<string, unknown> =>
  v !== null && typeof v === "object" && !Array.isArray(v);

export const isNum = (v: unknown): v is number =>
  typeof v === "number" && Number.isFinite(v);

export const isStr = (v: unknown): v is string => typeof v === "string";

export const isStrArr = (v: unknown): v is string[] =>
  Array.isArray(v) && v.every(isStr);

export function parseCamera(v: unknown): Camera | LegacyCameraBox | undefined {
  if (!isObj(v)) return undefined;
  if (!isNum(v.x) || !isNum(v.y)) return undefined;
  if (isNum(v.zoom)) return { x: v.x, y: v.y, zoom: v.zoom };
  if (isNum(v.w) && isNum(v.h) && v.w > 0 && v.h > 0) {
    return { x: v.x, y: v.y, w: v.w, h: v.h };
  }
  return undefined;
}

export function parseSavedView(v: unknown): SavedView | undefined {
  if (!isObj(v)) return undefined;
  if (!isStr(v.name) || !isStrArr(v.nodeIds)) return undefined;
  const out: SavedView = { name: v.name, nodeIds: v.nodeIds };
  if (isObj(v.viewport) && isNum(v.viewport.x) && isNum(v.viewport.y) &&
      isNum(v.viewport.w) && isNum(v.viewport.h)) {
    out.viewport = { x: v.viewport.x, y: v.viewport.y, w: v.viewport.w, h: v.viewport.h };
  }
  return out;
}

export function parseFold(v: unknown): Fold | undefined {
  if (!isObj(v)) return undefined;
  if (!isStr(v.id) || !isStr(v.label) || !isStrArr(v.memberIds)) return undefined;
  if (!Array.isArray(v.position) || v.position.length !== 2 ||
      !isNum(v.position[0]) || !isNum(v.position[1])) return undefined;
  if (typeof v.collapsed !== "boolean") return undefined;
  return {
    id: v.id, label: v.label, memberIds: v.memberIds,
    position: [v.position[0], v.position[1]], collapsed: v.collapsed,
  };
}

export function collect<T>(v: unknown, parse: (x: unknown) => T | undefined): T[] | undefined {
  if (!Array.isArray(v)) return undefined;
  const out: T[] = [];
  for (const item of v) {
    const p = parse(item);
    if (p) out.push(p);
  }
  return out;
}

export function parseNodeViews(v: unknown): Record<string, NodeView> | undefined {
  if (!isObj(v)) return undefined;
  const out: Record<string, NodeView> = {};
  for (const [id, raw] of Object.entries(v)) {
    if (!isObj(raw) || !isNum(raw.x) || !isNum(raw.y)) continue;
    const nv: NodeView = { x: raw.x, y: raw.y };
    if (isStr(raw.sublabel)) nv.sublabel = raw.sublabel;
    if (isObj(raw.state)) nv.state = raw.state as Record<string, StateValue>;
    out[id] = nv;
  }
  return out;
}

export function parseEdgeViews(v: unknown): Record<string, EdgeView> | undefined {
  if (!isObj(v)) return undefined;
  const out: Record<string, EdgeView> = {};
  for (const [id, raw] of Object.entries(v)) {
    if (!isObj(raw)) continue;
    const ev: EdgeView = {};
    if (raw.route === "line" || raw.route === "snake" || raw.route === "snake-v" || raw.route === "below") {
      ev.route = raw.route;
    }
    out[id] = ev;
  }
  return out;
}
