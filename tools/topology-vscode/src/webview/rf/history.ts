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

/** Remove transient run-state fields so they never enter undo/redo snapshots. */
function stripTransient(nodes: RFNode[], edges: RFEdge[]): { nodes: RFNode[]; edges: RFEdge[] } {
  const strippedNodes = nodes.map((n) => {
    const d = { ...n.data };
    delete d.lastFire;
    delete d.slots;
    return { ...n, data: d };
  });
  const strippedEdges = edges.map((e) => {
    const d = { ...e.data };
    delete d.pulse;
    return { ...e, data: d };
  });
  return { nodes: strippedNodes, edges: strippedEdges };
}

function currentSnapshot(): Snapshot {
  return cloneSnapshot({ nodes: _rf!.getNodes(), edges: _rf!.getEdges(), viewerState });
}

export function pushSnapshot() {
  if (!_rf) return;
  const { nodes, edges } = _rf.toObject();
  const stripped = stripTransient(nodes, edges);
  past.push(cloneSnapshot({ nodes: stripped.nodes, edges: stripped.edges, viewerState }));
  if (past.length > HISTORY_LIMIT) past.shift();
  // Any new action clears the redo stack.
  future = [];
}

/** Overlay live transient fields from the current RF state onto restored nodes/edges. */
function overlayTransient(restoredNodes: RFNode[], restoredEdges: RFEdge[]): { nodes: RFNode[]; edges: RFEdge[] } {
  const liveNodes = _rf!.getNodes();
  const liveEdges = _rf!.getEdges();
  const liveNodeMap = new Map(liveNodes.map((n) => [n.id, n.data]));
  const liveEdgeMap = new Map(liveEdges.map((e) => [e.id, e.data]));
  const nodes = restoredNodes.map((n) => {
    const live = liveNodeMap.get(n.id);
    if (!live) return n;
    const overlay: Record<string, unknown> = {};
    if (live.lastFire !== undefined) overlay.lastFire = live.lastFire;
    if (live.slots !== undefined) overlay.slots = live.slots;
    return { ...n, data: { ...n.data, ...overlay } };
  });
  const edges = restoredEdges.map((e) => {
    const live = liveEdgeMap.get(e.id);
    if (!live) return e;
    const overlay: Record<string, unknown> = {};
    if (live.pulse !== undefined) overlay.pulse = live.pulse;
    return { ...e, data: { ...e.data, ...overlay } };
  });
  return { nodes, edges };
}

export function undo() {
  if (!_rf || past.length === 0) return;
  const curr = currentSnapshot();
  const stripped = stripTransient(curr.nodes, curr.edges);
  future.push(cloneSnapshot({ nodes: stripped.nodes, edges: stripped.edges, viewerState: curr.viewerState }));
  const prev = past.pop()!;
  const { nodes, edges } = overlayTransient(prev.nodes, prev.edges);
  setViewerState(prev.viewerState);
  rfSetNodes(() => nodes);
  rfSetEdges(() => edges);
}

export function redo() {
  if (!_rf || future.length === 0) return;
  const curr = currentSnapshot();
  const stripped = stripTransient(curr.nodes, curr.edges);
  past.push(cloneSnapshot({ nodes: stripped.nodes, edges: stripped.edges, viewerState: curr.viewerState }));
  const next = future.pop()!;
  const { nodes, edges } = overlayTransient(next.nodes, next.edges);
  setViewerState(next.viewerState);
  rfSetNodes(() => nodes);
  rfSetEdges(() => edges);
}

export function canUndo() { return past.length > 0; }
export function canRedo() { return future.length > 0; }
