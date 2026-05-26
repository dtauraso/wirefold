// Viewer state — sidecar to topology.json. None of these fields belong in the
// spec; the runtime loader ignores them entirely. See visual-editor-plan.md §"Spec vs
// viewer state" for the policy.

import type { StateValue } from "../../../schema";
import {
  isObj, isStrArr,
  parseCamera, parseSavedView, parseFold,
  collect, parseNodeViews, parseEdgeViews,
} from "./parse";

// Canonical camera is React Flow's pan/zoom: `{x, y, zoom}`. The lit-html
// era persisted an SVG viewBox `{x, y, w, h}`; we still read those on load
// and migrate to canonical on the next save, but never write the legacy
// shape ourselves.
export type Camera = { x: number; y: number; zoom: number };
export type LegacyCameraBox = { x: number; y: number; w: number; h: number };

export function isLegacyCamera(c: Camera | LegacyCameraBox): c is LegacyCameraBox {
  return typeof (c as Camera).zoom !== "number";
}

export type SavedView = {
  name: string;
  // Legacy field — older sidecars captured the camera at save time. New
  // saves omit it; clicking a view frames its members via RF fitView.
  viewport?: { x: number; y: number; w: number; h: number };
  nodeIds: string[];
};

export type Fold = {
  id: string;
  label: string;
  memberIds: string[];
  position: [number, number];
  collapsed: boolean;
};

export type NodeView = {
  x: number;
  y: number;
  // 3D depth coordinate. Absent in legacy data — default to 0 on read.
  z?: number;
  sublabel?: string;
  state?: Record<string, StateValue>;
};

export type EdgeView = {
  route?: "line" | "snake" | "snake-v" | "below";
};

export type ViewerState = {
  camera?: Camera | LegacyCameraBox;
  views?: SavedView[];
  folds?: Fold[];
  lastSelectionIds?: string[];
  nodes?: Record<string, NodeView>;
  edges?: Record<string, EdgeView>;
};

export const DEFAULT_VIEWER_STATE: ViewerState = {};

export function parseViewerState(text: string | undefined): ViewerState {
  if (!text) return { ...DEFAULT_VIEWER_STATE };
  let raw: unknown;
  try {
    raw = JSON.parse(text);
  } catch (err) {
    console.warn("topology.view.json: invalid JSON, ignoring sidecar", err);
    return { ...DEFAULT_VIEWER_STATE };
  }
  if (!isObj(raw)) {
    console.warn("topology.view.json: top-level value is not an object, ignoring");
    return { ...DEFAULT_VIEWER_STATE };
  }
  const out: ViewerState = {};
  if (raw.camera !== undefined) {
    const cam = parseCamera(raw.camera);
    if (cam) out.camera = cam;
    else console.warn("topology.view.json: dropping malformed camera");
  }
  if (raw.views !== undefined) {
    const views = collect(raw.views, parseSavedView);
    if (views) out.views = views;
    else console.warn("topology.view.json: views is not an array, dropping");
  }
  if (raw.folds !== undefined) {
    const folds = collect(raw.folds, parseFold);
    if (folds) out.folds = folds;
    else console.warn("topology.view.json: folds is not an array, dropping");
  }
  if (raw.lastSelectionIds !== undefined) {
    if (isStrArr(raw.lastSelectionIds)) out.lastSelectionIds = raw.lastSelectionIds;
    else console.warn("topology.view.json: lastSelectionIds is not a string[], dropping");
  }
  if (raw.nodes !== undefined) {
    const nv = parseNodeViews(raw.nodes);
    if (nv) out.nodes = nv;
    else console.warn("topology.view.json: nodes is not a valid node-view map, dropping");
  }
  if (raw.edges !== undefined) {
    const ev = parseEdgeViews(raw.edges);
    if (ev) out.edges = ev;
    else console.warn("topology.view.json: edges is not a valid edge-view map, dropping");
  }
  return out;
}

export function serializeViewerState(s: ViewerState): string {
  return JSON.stringify(s, null, 2) + "\n";
}
