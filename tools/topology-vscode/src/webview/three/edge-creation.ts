// Edge-creation subsystem: pure function extracted from store.ts.
// The store action (createEdge) calls buildEdge and applies its result.

import type { RFNode, RFEdge, NodeData, EdgeData } from "../types";
import { KIND_COLORS, NODE_TYPES, type EdgeKind } from "../../schema";

export interface BuildEdgeResult {
  newEdge: RFEdge<EdgeData>;
  /** Deduplicated edge id (may differ from base id if collisions exist). */
  id: string;
}

/**
 * Pure edge-construction logic. Returns the new edge and its resolved id,
 * or null if the connection is invalid (with console.warn for the reason).
 * Does NOT mutate state — callers apply the result.
 */
export function buildEdge(
  nodes: RFNode<NodeData>[],
  edges: RFEdge<EdgeData>[],
  sourceId: string,
  sourceHandleIn: string | null,
  targetId: string,
  targetHandleIn: string | null,
): BuildEdgeResult | null {
  if (nodes.length === 0) return null;
  const srcRF = nodes.find((n) => n.id === sourceId);
  const dstRF = nodes.find((n) => n.id === targetId);
  if (!srcRF || !dstRF) return null;
  if (sourceId === targetId) {
    console.warn("[createEdge] Can't connect: source and target are the same node.");
    return null;
  }

  const srcType = (srcRF.data?.type ?? srcRF.type) as string;
  const dstType = (dstRF.data?.type ?? dstRF.type) as string;
  const srcDef = NODE_TYPES[srcType];
  const dstDef = NODE_TYPES[dstType];

  const srcOutputs: { name: string; kind: string }[] =
    srcDef?.outputs.length ? srcDef.outputs : (srcRF.data?.outputs ?? []);
  const dstInputs: { name: string; kind: string }[] =
    dstDef?.inputs.length ? dstDef.inputs : (dstRF.data?.inputs ?? []);

  const sourceHandle = sourceHandleIn ?? srcOutputs[0]?.name ?? null;
  const firstFree = targetHandleIn === null
    ? dstInputs.find((p) => !edges.some((e) => e.target === targetId && e.targetHandle === p.name))
    : undefined;
  const targetHandle = targetHandleIn ?? firstFree?.name ?? null;

  if (!sourceHandle || !targetHandle) {
    console.warn(
      `[createEdge] Can't connect ${sourceId} → ${targetId}: ` +
      `no resolvable port handle (sourceHandle=${sourceHandle}, targetHandle=${targetHandle}). ` +
      `Check that both nodes have outputs/inputs defined in NODE_TYPES or node data.`,
    );
    return null;
  }

  if (edges.some((e) => e.target === targetId && e.targetHandle === targetHandle)) {
    console.warn(
      `[createEdge] Can't connect: input "${targetHandle}" on node "${targetId}" is already wired. ` +
      `Disconnect the existing edge first.`,
    );
    return null;
  }

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
  while (edges.some((e) => e.id === id)) id = `${baseId}#${n++}`;

  const cap = (s: string) => (s.length === 0 ? s : s[0].toUpperCase() + s.slice(1));
  const baseLabel = `${sourceId}${cap(sourceHandle)}To${cap(targetId)}${cap(targetHandle)}`
    .replace(/[^A-Za-z0-9_]/g, "_")
    .replace(/^([0-9])/, "_$1");
  let label = baseLabel;
  let m = 2;
  while (edges.some((e) => e.data?.label === label)) label = `${baseLabel}_${m++}`;

  const newEdge: RFEdge<EdgeData> = {
    id,
    source: sourceId,
    sourceHandle,
    target: targetId,
    targetHandle,
    type: "substrate",
    style: { stroke: KIND_COLORS[kind] ?? "#888", strokeWidth: 1.5 },
    data: { kind, label, sourceHandle, targetHandle } as EdgeData,
  };

  return { newEdge, id };
}
