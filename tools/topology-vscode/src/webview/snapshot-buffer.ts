// snapshot-buffer.ts — module-level sinks for the latest binary frames from Go's dedicated
// per-owner stream fds (memory/feedback_no_single_writer_bridge.md). There is no fd-3
// SCENE/BEAD/NODE/EDGE fallback anymore — WIREFOLD_STREAM_FDS is mandatory
// (per-owner-buffer-rows.md's final step deleted Buffer.SnapshotState, the central
// accumulator that used to write those fd-3 frames).
//
// Separated from main.tsx so that buffer-scene.tsx / BeadInstances.tsx can read them
// without creating a circular import (main.tsx → ThreeView → buffer-scene → main).
// Not a Zustand store: these are plain module-level cells, identical in role to a
// global register — each holds exactly one pointer and never notifies beyond its own
// listener set.
//
// One singleton cell exists (VIEW), plus keyed maps for the per-row streams (edge/node/
// interior, below):
//   - the VIEW cell (setLatestViewFrame/getLatestViewFrame/subscribeViewFrame) — the
//     camera+overlay+scene frame arriving on its own dedicated pipe (VIEW_FD in
//     runCommand.ts), the first stream migrated off fd 3 per the no-single-writer-bridge
//     rule. Null until the first frame has landed.

let latestViewFrame: ArrayBuffer | null = null;

// Listeners are notified after each new snapshot lands. This is NOT a domain store —
// it is the same one-pointer cell as before; the listener set only lets a widget that
// must re-render on Go-owned change (e.g. the overlay toggle control, whose displayed
// state reflects the buffer's Go-owned overlay columns) subscribe via
// useSyncExternalStore instead of polling every frame. Nothing here authors state.
type SnapshotListener = () => void;
const viewListeners = new Set<SnapshotListener>();

/** Called by main.tsx whenever a new VIEW buffer-snapshot message arrives (the dedicated
 * view-fd stream — see this file's header comment). */
export function setLatestViewFrame(buf: ArrayBuffer): void {
  latestViewFrame = buf;
  for (const fn of viewListeners) fn();
}

/** Called by three/view-blocks.ts (and tests) to read the most-recent VIEW frame. Null
 * until the first frame has landed. */
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

// --- per-node dedicated streams (memory/feedback_no_single_writer_bridge.md) ---
//
// TWO keyed maps, mirroring edgeStreamFrames' role but split by stream KIND: one node's
// own nodeMover (geometry+ports+label) and its OWN Update goroutine (interior beads) write
// to two DIFFERENT fds (see Buffer/stream_fds.go's StreamKindNode/StreamKindInterior), so
// they get two separate cells here, both keyed by node row. version counters let
// node-stream-blocks.ts's aggregator memoize its (necessarily-copying) rebuild instead of
// re-concatenating every consumer's per-frame read.
const nodeStreamFrames: Map<number, ArrayBuffer> = new Map();
const interiorStreamFrames: Map<number, ArrayBuffer> = new Map();
const nodeStreamListeners = new Set<SnapshotListener>();
const interiorStreamListeners = new Set<SnapshotListener>();
let nodeStreamVersion = 0;
let interiorStreamVersion = 0;

/** Called by main.tsx whenever a new per-node dedicated NODE-stream frame arrives (tag
 *  BUF_BLOCK_TAG_NODE_STREAM, carrying `row`). */
export function setLatestNodeStreamFrame(row: number, buf: ArrayBuffer): void {
  nodeStreamFrames.set(row, buf);
  nodeStreamVersion++;
  for (const fn of nodeStreamListeners) fn();
}

/** Called by node-stream-blocks.ts (and tests) to read every node row's most-recent
 *  dedicated NODE-stream frame. Empty when the dedicated node-fd path is not active
 *  (fallback launches never populate this map). */
export function getLatestNodeStreamFrames(): ReadonlyMap<number, ArrayBuffer> {
  return nodeStreamFrames;
}

/** Monotonic counter bumped on every setLatestNodeStreamFrame call — a cheap memo key for
 *  the aggregator (rebuilding the aggregate DecodedNodeFrame is a full copy; this avoids
 *  redoing it when nothing changed since the last read). */
export function getNodeStreamVersion(): number {
  return nodeStreamVersion;
}

/** Subscribe to per-node NODE-stream arrivals (any row); returns an unsubscribe fn. */
export function subscribeNodeStreamFrame(fn: SnapshotListener): () => void {
  nodeStreamListeners.add(fn);
  return () => {
    nodeStreamListeners.delete(fn);
  };
}

/** Called by main.tsx whenever a new per-node dedicated INTERIOR-stream frame arrives (tag
 *  BUF_BLOCK_TAG_INTERIOR_STREAM, carrying `row`). */
export function setLatestInteriorStreamFrame(row: number, buf: ArrayBuffer): void {
  interiorStreamFrames.set(row, buf);
  interiorStreamVersion++;
  for (const fn of interiorStreamListeners) fn();
}

/** Called by node-stream-blocks.ts (and tests) to read every node row's most-recent
 *  dedicated INTERIOR-stream frame. */
export function getLatestInteriorStreamFrames(): ReadonlyMap<number, ArrayBuffer> {
  return interiorStreamFrames;
}

/** Monotonic counter mirroring getNodeStreamVersion for the interior stream. */
export function getInteriorStreamVersion(): number {
  return interiorStreamVersion;
}

/** Subscribe to per-node INTERIOR-stream arrivals (any row); returns an unsubscribe fn. */
export function subscribeInteriorStreamFrame(fn: SnapshotListener): () => void {
  interiorStreamListeners.add(fn);
  return () => {
    interiorStreamListeners.delete(fn);
  };
}
