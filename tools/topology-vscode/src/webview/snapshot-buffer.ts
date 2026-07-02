// snapshot-buffer.ts — module-level sink for the latest binary snapshot from Go's fd3.
//
// Separated from main.tsx so that buffer-scene.tsx can read it without
// creating a circular import (main.tsx → ThreeView → buffer-scene → main).
// Not a Zustand store: this is a plain module-level cell, identical in role
// to a global register — it holds exactly one pointer and never notifies.

let latestSnapshot: ArrayBuffer | null = null;

/** Called by main.tsx whenever a new buffer-snapshot message arrives. */
export function setLatestSnapshot(buf: ArrayBuffer): void {
  latestSnapshot = buf;
}

/** Called by buffer-scene.tsx (and tests) to read the most-recent snapshot. */
export function getLatestSnapshot(): ArrayBuffer | null {
  return latestSnapshot;
}
