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

// ---------------------------------------------------------------------------
// rfCreateEdge — imperative edge creation callable from outside React context.
// Replicates the core of _on-connect.ts without requiring AppCtx.
// sourceHandle / targetHandle: explicit port names. Pass null to auto-pick
// the first available output/input port on the node.
// Returns the new edge id, or null if creation was skipped.
// ---------------------------------------------------------------------------

import { KIND_COLORS, NODE_TYPES, type EdgeKind } from "../../schema";
import { scheduleSave } from "../save";
import { pushSnapshot } from "./history";

export function rfCreateEdge(
  sourceId: string,
  sourceHandleIn: string | null,
  targetId: string,
  targetHandleIn: string | null,
): string | null {
  if (_nodes.length === 0) return null; // no spec loaded yet
  const srcRF = _nodes.find((n) => n.id === sourceId);
  const dstRF = _nodes.find((n) => n.id === targetId);
  if (!srcRF || !dstRF) return null;
  if (sourceId === targetId) return null;

  const srcType = (srcRF.data?.type ?? srcRF.type) as string;
  const dstType = (dstRF.data?.type ?? dstRF.type) as string;
  const srcDef = NODE_TYPES[srcType];
  const dstDef = NODE_TYPES[dstType];

  // Resolve handles: prefer explicit, else first port from def then data
  const srcOutputs: { name: string; kind: string }[] =
    srcDef?.outputs.length ? srcDef.outputs : (srcRF.data?.outputs ?? []);
  const dstInputs: { name: string; kind: string }[] =
    dstDef?.inputs.length ? dstDef.inputs : (dstRF.data?.inputs ?? []);

  const sourceHandle = sourceHandleIn ?? srcOutputs[0]?.name ?? null;
  const targetHandle = targetHandleIn ?? dstInputs[0]?.name ?? null;

  // TODO(3d): multi-port disambiguation deferred per design doc
  if (!sourceHandle || !targetHandle) return null;

  // Reject if targetHandle already has an incoming edge (1-to-1 constraint)
  if (_edges.some((e) => e.target === targetId && e.targetHandle === targetHandle)) return null;

  const srcPort = srcDef?.outputs.find((p) => p.name === sourceHandle)
    ?? (srcRF.data?.outputs as { name: string; kind: string }[] | undefined)?.find((p) => p.name === sourceHandle);
  const dstPort = dstDef?.inputs.find((p) => p.name === targetHandle)
    ?? (dstRF.data?.inputs as { name: string; kind: string }[] | undefined)?.find((p) => p.name === targetHandle);

  const kind: EdgeKind = (srcPort && dstPort && srcPort.kind === dstPort.kind)
    ? (srcPort.kind as EdgeKind)
    : "any";

  const baseId = `${sourceId}.${sourceHandle}->${targetId}.${targetHandle}`;
  let id = baseId;
  let n = 2;
  while (_edges.some((e) => e.id === id)) id = `${baseId}#${n++}`;

  const cap = (s: string) => (s.length === 0 ? s : s[0].toUpperCase() + s.slice(1));
  const baseLabel = `${sourceId}${cap(sourceHandle)}To${cap(targetId)}${cap(targetHandle)}`
    .replace(/[^A-Za-z0-9_]/g, "_")
    .replace(/^([0-9])/, "_$1");
  let label = baseLabel;
  let m = 2;
  while (_edges.some((e) => e.data?.label === label)) label = `${baseLabel}_${m++}`;

  pushSnapshot();
  _setEdges?.((es) => [
    ...es,
    {
      id,
      source: sourceId,
      sourceHandle,
      target: targetId,
      targetHandle,
      type: "substrate",
      style: { stroke: KIND_COLORS[kind] ?? "#888", strokeWidth: 1.5 },
      data: { kind, label, sourceHandle, targetHandle },
    },
  ]);
  scheduleSave();
  return id;
}
