// R3F zustand store — single source of truth for node/edge state.
// save.ts reads useThreeStore.getState().nodes/edges directly (no mirror).

import { create } from "zustand";
import type { RFNode, RFEdge, NodeData, EdgeData } from "../types";
import { parseSpec, type Spec } from "../../schema";
import { specToFlow } from "../state/adapter/spec-to-flow";
import { viewerState, setViewerState, patchViewerState } from "../state/viewer-state";
import { parseViewerState } from "../state/viewer/types";
import { getFolds } from "../state/folds-state";
import { getDimmed } from "../state/dimmed";
import { KIND_COLORS, NODE_TYPES, type EdgeKind } from "../../schema";
import { scheduleSave, setSpecMeta, markViewSynced, scheduleViewSave } from "../save";
import { postLog } from "../log/post";
import { serializeViewerState } from "../state/viewer/types";
import { computeFade, type FadeEdge } from "./fade";
import { vscode } from "../vscode-api";
import { clearPulse } from "./pulse-state";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/**
 * Re-derive data.faded on every node/edge from the current fade sets.
 * Called after any rebuild that replaces the node/edge arrays.
 */
function applyFade(
  nodes: RFNode<NodeData>[],
  edges: RFEdge<EdgeData>[],
  directlyFadedNodes: Set<string>,
  directlyFadedEdges: Set<string>,
): { nodes: RFNode<NodeData>[]; edges: RFEdge<EdgeData>[] } {
  const nodeIds = nodes.map((n) => n.id);
  const fadeEdges: FadeEdge[] = edges.map((e) => ({ id: e.id, source: e.source, target: e.target }));
  const { fadedNodes, fadedEdges } = computeFade(nodeIds, fadeEdges, directlyFadedNodes, directlyFadedEdges);
  const nextNodes = nodes.map((n) => {
    const f = fadedNodes.has(n.id);
    if (!!n.data.faded === f) return n;
    return { ...n, data: { ...n.data, faded: f } };
  });
  const nextEdges = edges.map((e) => {
    const f = fadedEdges.has(e.id);
    if (!!(e.data?.faded) === f) return e;
    return { ...e, data: { ...(e.data ?? {}), faded: f } as typeof e.data };
  });
  return { nodes: nextNodes, edges: nextEdges };
}

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

  // --- Fade state ---
  directlyFadedNodes: Set<string>;
  directlyFadedEdges: Set<string>;

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
  saveSpec: () => void;
  /** Toggle fade on a node or edge. Recomputes fixpoint and emits updated faded-edge set to host. */
  toggleFade: (target: { kind: "node" | "edge"; id: string }) => void;
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
  directlyFadedNodes: new Set<string>(),
  directlyFadedEdges: new Set<string>(),

  loadSpec(specText: string) {
    try {
      const rawJson = JSON.parse(specText);
      const spec = parseSpec(rawJson);
      const flow = specToFlow(spec, getFolds(), viewerState, viewerState.lastSelectionIds ?? [], getDimmed());
      let nodes = flow.nodes as RFNode<NodeData>[];
      let edges = flow.edges as RFEdge<EdgeData>[];
      const { directlyFadedNodes, directlyFadedEdges } = get();
      ({ nodes, edges } = applyFade(nodes, edges, directlyFadedNodes, directlyFadedEdges));
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
    const restoredFadedNodes = new Set<string>(next.directlyFadedNodes ?? []);
    const restoredFadedEdges = new Set<string>(next.directlyFadedEdges ?? []);
    const lastSpec = get()._lastSpec;
    if (lastSpec) {
      const flow = specToFlow(lastSpec, getFolds(), next, next.lastSelectionIds ?? [], getDimmed());
      let nodes = flow.nodes as RFNode<NodeData>[];
      let edges = flow.edges as RFEdge<EdgeData>[];
      ({ nodes, edges } = applyFade(nodes, edges, restoredFadedNodes, restoredFadedEdges));
      set({
        nodes,
        edges,
        loadEpoch: get().loadEpoch + 1,
        directlyFadedNodes: restoredFadedNodes,
        directlyFadedEdges: restoredFadedEdges,
      });
      postLog("lifecycle", { phase: "store:view-load", nodes: nodes.length, edges: edges.length });
    } else {
      set({ directlyFadedNodes: restoredFadedNodes, directlyFadedEdges: restoredFadedEdges });
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

  saveSpec() {
    scheduleSave();
  },

  toggleFade({ kind, id }) {
    const { nodes, edges, directlyFadedNodes, directlyFadedEdges } = get();

    // Clone sets so we don't mutate the stored references.
    const nextFadedNodes = new Set<string>(directlyFadedNodes);
    const nextFadedEdges = new Set<string>(directlyFadedEdges);

    if (kind === "node") {
      if (nextFadedNodes.has(id)) nextFadedNodes.delete(id);
      else nextFadedNodes.add(id);
    } else {
      if (nextFadedEdges.has(id)) nextFadedEdges.delete(id);
      else nextFadedEdges.add(id);
    }

    // Compute which edges were previously unfaded so we can clear stale pulses.
    const prevFadedEdgeIds = new Set(
      edges.filter((e) => !!(e.data?.faded)).map((e) => e.id),
    );

    const { nodes: nextNodes, edges: nextEdges } = applyFade(nodes, edges, nextFadedNodes, nextFadedEdges);

    // Clear pulse state for any edge that is NEWLY faded this toggle.
    for (const e of nextEdges) {
      if (e.data?.faded && !prevFadedEdgeIds.has(e.id)) {
        clearPulse(e.id);
      }
    }

    // Derive fadedEdges set for the host message.
    const fadedEdges = new Set(nextEdges.filter((e) => !!(e.data?.faded)).map((e) => e.id));

    set({
      directlyFadedNodes: nextFadedNodes,
      directlyFadedEdges: nextFadedEdges,
      nodes: nextNodes,
      edges: nextEdges,
    });

    patchViewerState((v) => {
      v.directlyFadedNodes = [...nextFadedNodes];
      v.directlyFadedEdges = [...nextFadedEdges];
    });
    scheduleViewSave();

    // Emit the full faded-edge set to the host so Go can update its wire flags.
    vscode.postMessage({ type: "fade", edges: [...fadedEdges] });
  },
}));
