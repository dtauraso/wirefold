// snapshot-buffer.ts — module-level sink for the latest binary snapshot from Go's fd3.
//
// Separated from main.tsx so that buffer-scene.tsx can read it without
// creating a circular import (main.tsx → ThreeView → buffer-scene → main).
// Not a Zustand store: this is a plain module-level cell, identical in role
// to a global register — it holds exactly one pointer and never notifies.

let latestSnapshot: ArrayBuffer | null = null;

// Listeners are notified after each new snapshot lands. This is NOT a domain store —
// it is the same one-pointer cell as before; the listener set only lets a widget that
// must re-render on Go-owned change (e.g. the overlay toggle control, whose displayed
// state reflects the buffer's Go-owned overlay columns) subscribe via
// useSyncExternalStore instead of polling every frame. Nothing here authors state.
type SnapshotListener = () => void;
const listeners = new Set<SnapshotListener>();

/** Called by main.tsx whenever a new buffer-snapshot message arrives. */
export function setLatestSnapshot(buf: ArrayBuffer): void {
  latestSnapshot = buf;
  for (const fn of listeners) fn();
}

/** Called by buffer-scene.tsx (and tests) to read the most-recent snapshot. */
export function getLatestSnapshot(): ArrayBuffer | null {
  return latestSnapshot;
}

/** Subscribe to snapshot arrivals; returns an unsubscribe fn (useSyncExternalStore shape). */
export function subscribeSnapshot(fn: SnapshotListener): () => void {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
}
