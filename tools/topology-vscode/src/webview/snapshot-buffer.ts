// snapshot-buffer.ts — module-level sinks for the latest binary frames from Go's fd3.
//
// Separated from main.tsx so that buffer-scene.tsx / BeadInstances.tsx can read them
// without creating a circular import (main.tsx → ThreeView → buffer-scene → main).
// Not a Zustand store: these are plain module-level cells, identical in role to a
// global register — each holds exactly one pointer and never notifies beyond its own
// listener set.
//
// Five cells exist, mirroring the fd-3 block tags PLUS the dedicated VIEW-stream tag
// (schema/frame-tags.ts):
//   - the SCENE cell (setLatestSnapshot/getLatestSnapshot/subscribeSnapshot) — everything
//     except beads, the node-owner-group blocks, the Edge block, and (when the dedicated
//     view fd is active) camera/overlay/scene.
//   - the BEAD cell (setLatestBeadFrame/getLatestBeadFrame/subscribeBeadFrame) — the
//     self-contained per-tick Bead frame, read by BeadInstances.tsx instead of pulling
//     beads out of the combined snapshot (beads no longer ride it).
//   - the NODE cell (setLatestNodeFrame/getLatestNodeFrame/subscribeNodeFrame) — the
//     self-contained Node/Interior/Port (+ Label/PortName bytes) frame, read by every
//     renderer that used to pull those blocks out of the combined snapshot.
//   - the EDGE cell (setLatestEdgeFrame/getLatestEdgeFrame/subscribeEdgeFrame) — the
//     self-contained Edge (+ EdgeLabel bytes) frame, read by EdgeTube.tsx instead of
//     pulling the Edge block out of the combined snapshot.
//   - the VIEW cell (setLatestViewFrame/getLatestViewFrame/subscribeViewFrame) — the
//     camera+overlay+scene frame arriving on its OWN dedicated pipe (VIEW_FD in
//     runCommand.ts), the first stream migrated off fd 3 per the no-single-writer-bridge
//     rule (memory/feedback_no_single_writer_bridge.md). Null when the dedicated view fd
//     is not active (fallback launches) — see three/view-blocks.ts for the either/or read
//     that falls back to the SCENE cell's embedded camera/overlay/scene in that case.

let latestSnapshot: ArrayBuffer | null = null;
let latestBeadFrame: ArrayBuffer | null = null;
let latestNodeFrame: ArrayBuffer | null = null;
let latestEdgeFrame: ArrayBuffer | null = null;
let latestViewFrame: ArrayBuffer | null = null;

// Listeners are notified after each new snapshot lands. This is NOT a domain store —
// it is the same one-pointer cell as before; the listener set only lets a widget that
// must re-render on Go-owned change (e.g. the overlay toggle control, whose displayed
// state reflects the buffer's Go-owned overlay columns) subscribe via
// useSyncExternalStore instead of polling every frame. Nothing here authors state.
type SnapshotListener = () => void;
const listeners = new Set<SnapshotListener>();
const beadListeners = new Set<SnapshotListener>();
const nodeListeners = new Set<SnapshotListener>();
const edgeListeners = new Set<SnapshotListener>();
const viewListeners = new Set<SnapshotListener>();

/** Called by main.tsx whenever a new SCENE buffer-snapshot message arrives. */
export function setLatestSnapshot(buf: ArrayBuffer): void {
  latestSnapshot = buf;
  for (const fn of listeners) fn();
}

/** Called by buffer-scene.tsx (and tests) to read the most-recent scene snapshot. */
export function getLatestSnapshot(): ArrayBuffer | null {
  return latestSnapshot;
}

/** Subscribe to scene snapshot arrivals; returns an unsubscribe fn (useSyncExternalStore shape). */
export function subscribeSnapshot(fn: SnapshotListener): () => void {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
}

/** Called by main.tsx whenever a new BEAD buffer-snapshot message arrives. */
export function setLatestBeadFrame(buf: ArrayBuffer): void {
  latestBeadFrame = buf;
  for (const fn of beadListeners) fn();
}

/** Called by BeadInstances.tsx (and tests) to read the most-recent bead frame. */
export function getLatestBeadFrame(): ArrayBuffer | null {
  return latestBeadFrame;
}

/** Subscribe to bead-frame arrivals; returns an unsubscribe fn (useSyncExternalStore shape). */
export function subscribeBeadFrame(fn: SnapshotListener): () => void {
  beadListeners.add(fn);
  return () => {
    beadListeners.delete(fn);
  };
}

/** Called by main.tsx whenever a new NODE buffer-snapshot message arrives. */
export function setLatestNodeFrame(buf: ArrayBuffer): void {
  latestNodeFrame = buf;
  for (const fn of nodeListeners) fn();
}

/** Called by the node/port/interior/label renderers (and tests) to read the most-recent
 * node-owner-group frame. */
export function getLatestNodeFrame(): ArrayBuffer | null {
  return latestNodeFrame;
}

/** Subscribe to node-frame arrivals; returns an unsubscribe fn (useSyncExternalStore shape). */
export function subscribeNodeFrame(fn: SnapshotListener): () => void {
  nodeListeners.add(fn);
  return () => {
    nodeListeners.delete(fn);
  };
}

/** Called by main.tsx whenever a new EDGE buffer-snapshot message arrives. */
export function setLatestEdgeFrame(buf: ArrayBuffer): void {
  latestEdgeFrame = buf;
  for (const fn of edgeListeners) fn();
}

/** Called by EdgeTube.tsx (and tests) to read the most-recent Edge frame. */
export function getLatestEdgeFrame(): ArrayBuffer | null {
  return latestEdgeFrame;
}

/** Subscribe to edge-frame arrivals; returns an unsubscribe fn (useSyncExternalStore shape). */
export function subscribeEdgeFrame(fn: SnapshotListener): () => void {
  edgeListeners.add(fn);
  return () => {
    edgeListeners.delete(fn);
  };
}

/** Called by main.tsx whenever a new VIEW buffer-snapshot message arrives (the dedicated
 * view-fd stream — see this file's header comment). */
export function setLatestViewFrame(buf: ArrayBuffer): void {
  latestViewFrame = buf;
  for (const fn of viewListeners) fn();
}

/** Called by three/view-blocks.ts (and tests) to read the most-recent VIEW frame. Null
 * when the dedicated view fd is not active (fallback launches never populate this cell). */
export function getLatestViewFrame(): ArrayBuffer | null {
  return latestViewFrame;
}

/** Subscribe to VIEW-frame arrivals; returns an unsubscribe fn (useSyncExternalStore shape). */
export function subscribeViewFrame(fn: SnapshotListener): () => void {
  viewListeners.add(fn);
  return () => {
    viewListeners.delete(fn);
  };
}

// --- per-edge dedicated streams (memory/feedback_no_single_writer_bridge.md) ---
//
// Unlike the singleton VIEW stream, there are MANY per-edge streams (one per edge row —
// see Buffer/stream_fds.go's StreamKindEdge / runCommand.ts's edge-fd range). Keyed by
// edge row (int) rather than one bare cell. Still a plain module-level Map, not a store —
// same one-pointer-per-key role as the cells above, just keyed.
const edgeStreamFrames: Map<number, ArrayBuffer> = new Map();
const edgeStreamListeners = new Set<SnapshotListener>();

/** Called by main.tsx whenever a new per-edge dedicated-stream frame arrives (tag
 *  BUF_BLOCK_TAG_EDGE_STREAM, carrying `row`). */
export function setLatestEdgeStreamFrame(row: number, buf: ArrayBuffer): void {
  edgeStreamFrames.set(row, buf);
  for (const fn of edgeStreamListeners) fn();
}

/** Called by EdgeTube.tsx/BeadInstances.tsx to read every edge row's most-recent dedicated
 *  per-edge frame. Empty when the dedicated edge-fd path is not active (fallback launches
 *  never populate this map) — callers fall back to the single EDGE/BEAD cells above in
 *  that case. */
export function getLatestEdgeStreamFrames(): ReadonlyMap<number, ArrayBuffer> {
  return edgeStreamFrames;
}

/** Subscribe to per-edge dedicated-stream arrivals (any row); returns an unsubscribe fn
 *  (useSyncExternalStore shape). */
export function subscribeEdgeStreamFrame(fn: SnapshotListener): () => void {
  edgeStreamListeners.add(fn);
  return () => {
    edgeStreamListeners.delete(fn);
  };
}
