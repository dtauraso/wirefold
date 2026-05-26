// Imperative RF handle — registered by Inner() on mount so non-React modules
// (inline-edit.ts, etc.) can call setNodes/setEdges without hooks.
// Callers must guard against null (before Inner mounts or after unmount).

import type { Dispatch, SetStateAction } from "react";
import type { Edge as RFEdge, Node as RFNode } from "reactflow";

type SetNodes = Dispatch<SetStateAction<RFNode[]>>;
type SetEdges = Dispatch<SetStateAction<RFEdge[]>>;

let _setNodes: SetNodes | null = null;
let _setEdges: SetEdges | null = null;
let _nodes: RFNode[] = [];
let _edges: RFEdge[] = [];

export function registerRFSetters(sn: SetNodes, se: SetEdges) {
  _setNodes = sn;
  _setEdges = se;
}

type RFStateListener = (nodes: RFNode[], edges: RFEdge[]) => void;
const _listeners = new Set<RFStateListener>();

export function subscribeRFState(fn: RFStateListener): () => void {
  _listeners.add(fn);
  return () => _listeners.delete(fn);
}

// Called by Inner() after each nodes/edges state change to keep the
// module-level snapshots current for rfGetNodes/rfGetEdges.
export function notifyRFState(nodes: RFNode[], edges: RFEdge[]) {
  _nodes = nodes;
  _edges = edges;
  for (const fn of _listeners) fn(nodes, edges);
}

export function rfSetNodes(updater: (ns: RFNode[]) => RFNode[]) {
  _setNodes?.(updater);
}

export function rfSetEdges(updater: (es: RFEdge[]) => RFEdge[]) {
  _setEdges?.(updater);
}

// Read the latest RF node/edge snapshots without hooks.
// Returns empty arrays before Inner mounts.
export function rfGetNodes(): RFNode[] {
  return _nodes;
}

export function rfGetEdges(): RFEdge[] {
  return _edges;
}
