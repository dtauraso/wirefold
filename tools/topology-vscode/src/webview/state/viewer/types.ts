// Viewer state — sidecar to topology.json. None of these fields belong in the
// spec; the runtime loader ignores them entirely. See visual-editor-plan.md §"Spec vs
// viewer state" for the policy.

import type { StateValue } from "../../../schema";
import {
  isObj, isStrArr,
  parseCamera, parseCamera3d,
  parseNodeViews, parseEdgeViews,
} from "./parse";

// Canonical camera is React Flow's pan/zoom: `{x, y, zoom}`. The lit-html
// era persisted an SVG viewBox `{x, y, w, h}`; we still read those on load
// and migrate to canonical on the next save, but never write the legacy
// shape ourselves.
export type Camera = { x: number; y: number; zoom: number };
export type LegacyCameraBox = { x: number; y: number; w: number; h: number };

// 3-D camera state: position (world space) + orientation quaternion.
// Stored as plain number arrays for JSON round-trip simplicity.
export type Camera3D = {
  position: [number, number, number];
  quaternion: [number, number, number, number]; // x y z w
};

export function isLegacyCamera(c: Camera | LegacyCameraBox): c is LegacyCameraBox {
  return typeof (c as Camera).zoom !== "number";
}

export type NodeView = {
  x: number;
  y: number;
  // 3D depth coordinate. Absent in legacy data — default to 0 on read.
  z?: number;
  state?: Record<string, StateValue>;
};

export type EdgeView = {
  route?: "line" | "snake" | "snake-v" | "below";
};

export type ViewerState = {
  camera?: Camera | LegacyCameraBox;
  camera3d?: Camera3D;
  lastSelectionIds?: string[];
  nodes?: Record<string, NodeView>;
  edges?: Record<string, EdgeView>;
  directlyFadedNodes?: string[];
  directlyFadedEdges?: string[];
  // Faded-edge ids in fade order (oldest → newest). Drives reverse-playback unfade.
  fadeEdgeOrder?: string[];
  labelsGlobalHidden?: boolean;
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
  if (raw.camera3d !== undefined) {
    const cam3d = parseCamera3d(raw.camera3d);
    if (cam3d) out.camera3d = cam3d;
    else console.warn("topology.view.json: dropping malformed camera3d");
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
  if (raw.directlyFadedNodes !== undefined) {
    if (isStrArr(raw.directlyFadedNodes)) out.directlyFadedNodes = raw.directlyFadedNodes;
    else console.warn("topology.view.json: directlyFadedNodes is not a string[], dropping");
  }
  if (raw.directlyFadedEdges !== undefined) {
    if (isStrArr(raw.directlyFadedEdges)) out.directlyFadedEdges = raw.directlyFadedEdges;
    else console.warn("topology.view.json: directlyFadedEdges is not a string[], dropping");
  }
  if (raw.fadeEdgeOrder !== undefined) {
    if (isStrArr(raw.fadeEdgeOrder)) out.fadeEdgeOrder = raw.fadeEdgeOrder;
    else console.warn("topology.view.json: fadeEdgeOrder is not a string[], dropping");
  }
  if (raw.labelsGlobalHidden === true) out.labelsGlobalHidden = true;
  return out;
}

export function serializeViewerState(s: ViewerState): string {
  return JSON.stringify(s, null, 2) + "\n";
}

// Scene-only fields (camera, camera3d, labelsGlobalHidden) — for topology.scene.json.
export type SceneState = Pick<ViewerState, "camera" | "camera3d" | "labelsGlobalHidden">;

export function serializeSceneState(s: ViewerState): string {
  const scene: SceneState = {};
  if (s.camera !== undefined) scene.camera = s.camera;
  if (s.camera3d !== undefined) scene.camera3d = s.camera3d;
  if (s.labelsGlobalHidden !== undefined) scene.labelsGlobalHidden = s.labelsGlobalHidden;
  return JSON.stringify(scene, null, 2) + "\n";
}

// Merge scene fields from a parsed flat scene object into a ViewerState.
// Returns a new ViewerState with scene fields overlaid.
export function mergeSceneIntoViewerState(base: ViewerState, sceneParsed: ViewerState): ViewerState {
  const out: ViewerState = { ...base };
  if (sceneParsed.camera !== undefined) out.camera = sceneParsed.camera;
  if (sceneParsed.camera3d !== undefined) out.camera3d = sceneParsed.camera3d;
  if (sceneParsed.labelsGlobalHidden !== undefined) out.labelsGlobalHidden = sceneParsed.labelsGlobalHidden;
  else delete out.labelsGlobalHidden; // absent = false (labels shown)
  return out;
}
