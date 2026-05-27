// Module-level viewer-state binding — mirrors rf-imperative.ts pattern so
// non-React callers (save.ts, inline-edit.ts, etc.) can read/write viewer
// state without going through Zustand or store.ts.
//
// Replaces the viewerState/setViewerState exports from state/store.ts +
// state/spec/mutators.ts now that the plain-module state files are gone.

import { produce } from "immer";
import { DEFAULT_VIEWER_STATE, type ViewerState } from "./viewer/types";

export let viewerState: ViewerState = { ...DEFAULT_VIEWER_STATE };

export function setViewerState(next: ViewerState) {
  viewerState = next;
}

// Lightweight patch for incidental viewer fields (camera, selection, fold position).
export function patchViewerState(fn: (v: ViewerState) => void) {
  viewerState = produce(viewerState, fn);
}

export function mutateViewer<T>(fn: (v: ViewerState) => T): T {
  let result!: T;
  const next = produce(viewerState, (draft) => {
    result = fn(draft) as T;
  });
  if (next !== viewerState) viewerState = next;
  return result;
}
