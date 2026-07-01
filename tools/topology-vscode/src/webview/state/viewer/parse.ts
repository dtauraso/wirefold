// Parser helpers for parseViewerState (viewerState.ts owns types + public API).

import type { StateValue } from "../../../schema";
import { isObj } from "../../../schema/parse-primitives";
import type {
  Camera, Camera3D, EdgeView, LegacyCameraBox,
  NodeView,
} from "./types";
import type { PolarCamera } from "../../three/camera-store";

export { isObj };

const isNum = (v: unknown): v is number =>
  typeof v === "number" && Number.isFinite(v);

const isStr = (v: unknown): v is string => typeof v === "string";

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

export function parseCamera3d(v: unknown): Camera3D | undefined {
  if (!isObj(v)) return undefined;
  const pos = v.position;
  const quat = v.quaternion;
  if (
    !Array.isArray(pos) || pos.length !== 3 || !pos.every(isNum) ||
    !Array.isArray(quat) || quat.length !== 4 || !quat.every(isNum)
  ) return undefined;
  return {
    position: [pos[0] as number, pos[1] as number, pos[2] as number],
    quaternion: [quat[0] as number, quat[1] as number, quat[2] as number, quat[3] as number],
  };
}

export function parsePolarCamera(v: unknown): PolarCamera | undefined {
  if (!isObj(v)) return undefined;
  const pivot = v.pivot;
  const pos = v.pos;
  const up = v.up;
  if (
    !Array.isArray(pivot) || pivot.length !== 3 || !pivot.every(isNum) ||
    !isNum(v.r) ||
    !Array.isArray(pos) || pos.length !== 2 || !pos.every(isNum) ||
    !Array.isArray(up) || up.length !== 2 || !up.every(isNum)
  ) return undefined;
  return {
    pivot: [pivot[0] as number, pivot[1] as number, pivot[2] as number],
    r: v.r as number,
    pos: [pos[0] as number, pos[1] as number],
    up: [up[0] as number, up[1] as number],
  };
}

export function parseNodeViews(v: unknown): Record<string, NodeView> | undefined {
  if (!isObj(v)) return undefined;
  const out: Record<string, NodeView> = {};
  for (const [id, raw] of Object.entries(v)) {
    if (!isObj(raw) || !isNum(raw.x) || !isNum(raw.y)) continue;
    const nv: NodeView = { x: raw.x, y: raw.y };
    if (isNum(raw.z)) nv.z = raw.z;
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
