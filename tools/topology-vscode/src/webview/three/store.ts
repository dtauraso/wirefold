// R3F zustand store — single source of truth for node/edge state.
// save.ts reads useThreeStore.getState().nodes/edges directly (no mirror).

import { create } from "zustand";
import type { RFNode, RFEdge, NodeData, EdgeData } from "../types";
import { parseSpec, type Spec } from "../../schema";
import { specToFlow } from "../state/adapter/spec-to-flow";
import { viewerState, setViewerState } from "../state/viewer-state";
import { parseViewerState } from "../state/viewer/types";
import { getFolds } from "../state/folds-state";
import { getDimmed } from "../state/dimmed";
import { KIND_COLORS, NODE_TYPES, type EdgeKind } from "../../schema";
import { scheduleSave, setSpecMeta, markViewSynced } from "../save";
import { postLog } from "../log/post";
import { serializeViewerState } from "../state/viewer/types";

// ---------------------------------------------------------------------------
// State shape
// ---------------------------------------------------------------------------

export interface ThreeStoreState {
  nodes: RFNode<NodeData>[];
  edges: RFEdge<EdgeData>[];
  selectedId: string | null;
  // Cached spec for re-running specToFlow after a view-load.
  _lastSpec: Spec | null;
  // Incremented each time content is (re)loaded; used to trigger camera re-fit.
  loadEpoch: number;

  // --- Actions ---
  loadSpec: (specText: string) => void;
  loadView: (viewText: string | undefined) => void;
  setNodes: (updater: RFNode<NodeData>[] | ((ns: RFNode<NodeData>[]) => RFNode<NodeData>[])) => void;
  setEdges: (updater: RFEdge<EdgeData>[] | ((es: RFEdge<EdgeData>[]) => RFEdge<EdgeData>[])) => void;
  setSelected: (id: string | null) => void;
  createEdge: (
    sourceId: string,
    sourceHandle: string | null,
    targetId: string,
    targetHandle: string | null,
  ) => string | null;
  moveNode: (id: string, x: number, y: number) => void;
  restoreNodesEdges: (nodes: RFNode<NodeData>[], edges: RFEdge<EdgeData>[]) => void;
  saveSpec: () => void;
}

// ---------------------------------------------------------------------------
// Store
// ---------------------------------------------------------------------------

export const useThreeStore = create<ThreeStoreState>((set, get) => ({
  nodes: [],
  edges: [],
  selectedId: null,
  _lastSpec: null,
  loadEpoch: 0,

  loadSpec(specText: string) {
    try {
      const rawJson = JSON.parse(specText);
      const spec = parseSpec(rawJson);
      const flow = specToFlow(spec, getFolds(), viewerState, viewerState.lastSelectionIds ?? [], getDimmed());
      const nodes = flow.nodes as RFNode<NodeData>[];
      const edges = flow.edges as RFEdge<EdgeData>[];
      set({ nodes, edges, _lastSpec: spec, loadEpoch: get().loadEpoch + 1 });
      setSpecMeta(spec);
      postLog("lifecycle", { phase: "store:load", nodes: nodes.length, edges: edges.length });
    } catch (err) {
      console.error("[ThreeStore] loadSpec failed", err);
    }
  },

  loadView(viewText: string | undefined) {
    const next = parseViewerState(viewText);
    setViewerState(next);
    markViewSynced(serializeViewerState(next));
    const lastSpec = get()._lastSpec;
    if (lastSpec) {
      const flow = specToFlow(lastSpec, getFolds(), next, next.lastSelectionIds ?? [], getDimmed());
      const nodes = flow.nodes as RFNode<NodeData>[];
      const edges = flow.edges as RFEdge<EdgeData>[];
      set({ nodes, edges, loadEpoch: get().loadEpoch + 1 });
      postLog("lifecycle", { phase: "store:view-load", nodes: nodes.length, edges: edges.length });
    } else {
      postLog("lifecycle", { phase: "store:view-load-noop" });
    }
  },

  setNodes(updater) {
    const next = typeof updater === "function" ? updater(get().nodes) : updater;
    set({ nodes: next });
  },

  setEdges(updater) {
    const next = typeof updater === "function" ? updater(get().edges) : updater;
    set({ edges: next });
  },

  setSelected(id) {
    set({ selectedId: id });
  },

  createEdge(sourceId, sourceHandleIn, targetId, targetHandleIn) {
    const { nodes, edges } = get();
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
    const targetHandle = targetHandleIn ?? dstInputs[0]?.name ?? null;

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

    const nextEdges = [...edges, newEdge];
    set({ edges: nextEdges });
    scheduleSave();
    return id;
  },

  moveNode(id, x, y) {
    const nextNodes = get().nodes.map((n) =>
      n.id === id ? { ...n, position: { x, y } } : n,
    );
    set({ nodes: nextNodes });
  },

  // Wholesale replace nodes+edges (undo/redo restore). Persists the result.
  restoreNodesEdges(nodes, edges) {
    set({ nodes, edges });
    scheduleSave();
  },

  saveSpec() {
    scheduleSave();
  },
}));
