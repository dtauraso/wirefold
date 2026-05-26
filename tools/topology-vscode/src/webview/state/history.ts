// Snapshot-based undo/redo. Backed by the zustand store (useThreeStore),
// which is the sole source of truth for node/edge state post-R3F-cutover.
//
// Usage:
//   pushSnapshot()  — call after any mutation that changes nodes/edges
//   undo() / redo() — restore previous/next state into the store
//
// (registerHistory() is retained as a no-op for callers that still invoke it;
// the store is read/written directly so no instance handle is needed.)

import type { RFNode, RFEdge } from "../types";
import { useThreeStore } from "../three/store";
import { viewerState, setViewerState } from "./viewer-state";
import type { ViewerState } from "./viewer/types";

const HISTORY_LIMIT = 50;

interface Snapshot {
  nodes: RFNode[];
  edges: RFEdge[];
  viewerState: ViewerState;
}

let past: Snapshot[] = [];
let future: Snapshot[] = [];

export function registerHistory() {
  // No-op: the store is the source of truth; kept for call-site compatibility.
}

function cloneSnapshot(s: Snapshot): Snapshot {
  return structuredClone(s);
}

function currentSnapshot(): Snapshot {
  const { nodes, edges } = useThreeStore.getState();
  return cloneSnapshot({ nodes, edges, viewerState });
}

export function pushSnapshot() {
  past.push(currentSnapshot());
  if (past.length > HISTORY_LIMIT) past.shift();
  // Any new action clears the redo stack.
  future = [];
}

export function undo() {
  if (past.length === 0) return;
  future.push(currentSnapshot());
  const prev = past.pop()!;
  setViewerState(prev.viewerState);
  useThreeStore.getState().restoreNodesEdges(
    prev.nodes as RFNode[],
    prev.edges as RFEdge[],
  );
}

export function redo() {
  if (future.length === 0) return;
  past.push(currentSnapshot());
  const next = future.pop()!;
  setViewerState(next.viewerState);
  useThreeStore.getState().restoreNodesEdges(
    next.nodes as RFNode[],
    next.edges as RFEdge[],
  );
}

export function canUndo() { return past.length > 0; }
export function canRedo() { return future.length > 0; }
