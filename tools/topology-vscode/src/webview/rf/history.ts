// RF-snapshot-based undo/redo. Replaces the 4 paired Zustand stacks
// (undoSpec/redoSpec/undoViewer/redoViewer) with a single history backed
// by RF's toObject() snapshot.
//
// Usage:
//   registerHistory(rf)  — call once in Inner() on mount
//   pushSnapshot()       — call after any mutation that changes nodes/edges
//   undo() / redo()      — restore previous/next RF state

import type { ReactFlowInstance, Node as RFNode, Edge as RFEdge } from "reactflow";
import { rfSetNodes, rfSetEdges } from "./rf-imperative";
import { viewerState, setViewerState } from "./viewer-state";
import type { ViewerState } from "../state/viewer/types";

const HISTORY_LIMIT = 50;

interface Snapshot {
  nodes: RFNode[];
  edges: RFEdge[];
  viewerState: ViewerState;
}

let past: Snapshot[] = [];
let future: Snapshot[] = [];
let _rf: ReactFlowInstance | null = null;

export function registerHistory(rf: ReactFlowInstance) {
  _rf = rf;
}

function cloneSnapshot(s: Snapshot): Snapshot {
  return structuredClone(s);
}

function currentSnapshot(): Snapshot {
  return cloneSnapshot({ nodes: _rf!.getNodes(), edges: _rf!.getEdges(), viewerState });
}

export function pushSnapshot() {
  if (!_rf) return;
  const { nodes, edges } = _rf.toObject();
  past.push(cloneSnapshot({ nodes, edges, viewerState }));
  if (past.length > HISTORY_LIMIT) past.shift();
  // Any new action clears the redo stack.
  future = [];
}

export function undo() {
  if (!_rf || past.length === 0) return;
  future.push(currentSnapshot());
  const prev = past.pop()!;
  setViewerState(cloneSnapshot(prev).viewerState);
  rfSetNodes(() => cloneSnapshot(prev).nodes);
  rfSetEdges(() => cloneSnapshot(prev).edges);
}

export function redo() {
  if (!_rf || future.length === 0) return;
  past.push(currentSnapshot());
  const next = future.pop()!;
  setViewerState(cloneSnapshot(next).viewerState);
  rfSetNodes(() => cloneSnapshot(next).nodes);
  rfSetEdges(() => cloneSnapshot(next).edges);
}

export function canUndo() { return past.length > 0; }
export function canRedo() { return future.length > 0; }
