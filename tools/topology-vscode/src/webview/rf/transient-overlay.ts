// Shared transient-field helpers used by both history.ts (undo/redo) and
// _handle-load.ts (file round-trip rebuilds).
//
// "Transient" fields carry in-flight run state that must never enter undo/redo
// snapshots, but must survive a spec-driven rebuild so a save mid-run does
// not wipe animation/slot state.

import type { ReactFlowInstance, Node as RFNode, Edge as RFEdge } from "reactflow";

export const TRANSIENT_NODE_FIELDS = ["lastFire", "slots"] as const;
export const TRANSIENT_EDGE_FIELDS = ["pulse"] as const;

let _rf: ReactFlowInstance | null = null;

/** Call once on mount (from Inner()) before using overlayTransient. */
export function registerRf(rf: ReactFlowInstance) {
  _rf = rf;
}

/** Deep-clone nodes/edges with transient fields removed. */
export function stripTransient(
  nodes: RFNode[],
  edges: RFEdge[],
): { nodes: RFNode[]; edges: RFEdge[] } {
  const strippedNodes = nodes.map((n) => {
    const d = { ...n.data };
    for (const f of TRANSIENT_NODE_FIELDS) delete d[f];
    return { ...n, data: d };
  });
  const strippedEdges = edges.map((e) => {
    const d = { ...e.data };
    for (const f of TRANSIENT_EDGE_FIELDS) delete d[f];
    return { ...e, data: d };
  });
  return { nodes: strippedNodes, edges: strippedEdges };
}

/**
 * Overlay live transient fields from the current RF state onto
 * restored/rebuilt nodes and edges.
 *
 * No-ops cleanly when no RF instance is registered yet (initial load)
 * or when the live RF state is empty.
 */
export function overlayTransient(
  restoredNodes: RFNode[],
  restoredEdges: RFEdge[],
): { nodes: RFNode[]; edges: RFEdge[] } {
  if (!_rf) return { nodes: restoredNodes, edges: restoredEdges };
  const liveNodes = _rf.getNodes();
  const liveEdges = _rf.getEdges();
  if (liveNodes.length === 0 && liveEdges.length === 0) {
    return { nodes: restoredNodes, edges: restoredEdges };
  }
  const liveNodeMap = new Map(liveNodes.map((n) => [n.id, n.data]));
  const liveEdgeMap = new Map(liveEdges.map((e) => [e.id, e.data]));
  const nodes = restoredNodes.map((n) => {
    const live = liveNodeMap.get(n.id);
    if (!live) return n;
    const overlay: Record<string, unknown> = {};
    for (const f of TRANSIENT_NODE_FIELDS) {
      if (live[f] !== undefined) overlay[f] = live[f];
    }
    return { ...n, data: { ...n.data, ...overlay } };
  });
  const edges = restoredEdges.map((e) => {
    const live = liveEdgeMap.get(e.id);
    if (!live) return e;
    const overlay: Record<string, unknown> = {};
    for (const f of TRANSIENT_EDGE_FIELDS) {
      if (live[f] !== undefined) overlay[f] = live[f];
    }
    return { ...e, data: { ...e.data, ...overlay } };
  });
  return { nodes, edges };
}
