// R3F zustand store — single source of truth for node/edge state.
// save.ts reads useThreeStore.getState().nodes/edges directly (no mirror).

import { create } from "zustand";
import type { RFNode, RFEdge, NodeData, EdgeData } from "../types";
import { parseSpec } from "../../schema";
import { specToFlow } from "../state/adapter/spec-to-flow";
import { viewerState, setViewerState, patchViewerState } from "../state/viewer-state";
import { parseViewerState } from "../state/viewer/types";
import { scheduleSave, setSpecMeta, markViewSynced, scheduleViewSave } from "../save";
import { postLog } from "../log/post";
import { serializeViewerState } from "../state/viewer/types";
import { vscode } from "../vscode-api";
import { clearPulse } from "./pulse-state";
import { applyFade, reconcileFadeOrder, computeToggleFade } from "./fade-actions";
import { buildEdge } from "./edge-creation";

// ---------------------------------------------------------------------------
// State shape
// ---------------------------------------------------------------------------

export interface ThreeStoreState {
  nodes: RFNode<NodeData>[];
  edges: RFEdge<EdgeData>[];
  selectedId: string | null;
  // Incremented each time content is (re)loaded; used to trigger camera re-fit.
  loadEpoch: number;

  // --- Fade state ---
  directlyFadedNodes: Set<string>;
  directlyFadedEdges: Set<string>;
  // Faded-edge ids in fade order (oldest → newest). Single source of truth
  // for "which edge faded most recently" — drives the reverse-playback walk.
  fadeEdgeOrder: string[];

  // --- Actions ---
  load: (text: string) => void;
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
  loadEpoch: 0,
  directlyFadedNodes: new Set<string>(),
  directlyFadedEdges: new Set<string>(),
  fadeEdgeOrder: [],

  load(text: string) {
    try {
      const raw = JSON.parse(text);
      const spec = parseSpec(raw);
      const viewText = raw.view !== undefined ? JSON.stringify(raw.view) : undefined;
      const next = parseViewerState(viewText);
      setViewerState(next);
      markViewSynced(serializeViewerState(next));
      const restoredFadedNodes = new Set<string>(next.directlyFadedNodes ?? []);
      const restoredFadedEdges = new Set<string>(next.directlyFadedEdges ?? []);
      const flow = specToFlow(spec, next, next.lastSelectionIds ?? []);
      let nodes = flow.nodes as RFNode<NodeData>[];
      let edges = flow.edges as RFEdge<EdgeData>[];
      ({ nodes, edges } = applyFade(nodes, edges, restoredFadedNodes, restoredFadedEdges));
      const fadeEdgeOrder = reconcileFadeOrder(next.fadeEdgeOrder ?? [], edges);
      set({
        nodes,
        edges,
        loadEpoch: get().loadEpoch + 1,
        directlyFadedNodes: restoredFadedNodes,
        directlyFadedEdges: restoredFadedEdges,
        fadeEdgeOrder,
      });
      setSpecMeta(spec);
      postLog("lifecycle", { phase: "store:load", nodes: nodes.length, edges: edges.length });
    } catch (err) {
      console.error("[ThreeStore] load failed", err);
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
    const result = buildEdge(nodes, edges, sourceId, sourceHandleIn, targetId, targetHandleIn);
    if (!result) return null;
    const nextEdges = [...edges, result.newEdge];
    set({ edges: nextEdges });
    scheduleSave();
    return result.id;
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

  toggleFade(target) {
    const { nodes, edges, directlyFadedNodes, directlyFadedEdges, fadeEdgeOrder } = get();
    const result = computeToggleFade(
      { nodes, edges, directlyFadedNodes, directlyFadedEdges, fadeEdgeOrder },
      target,
    );

    // Clear pulse state for any edge that is NEWLY faded this toggle.
    for (const edgeId of result.newlyFadedEdgeIds) {
      clearPulse(edgeId);
    }

    set({
      directlyFadedNodes: result.nextFadedNodes,
      directlyFadedEdges: result.nextFadedEdges,
      fadeEdgeOrder: result.nextFadeEdgeOrder,
      nodes: result.nextNodes,
      edges: result.nextEdges,
    });

    patchViewerState((v) => {
      v.directlyFadedNodes = [...result.nextFadedNodes];
      v.directlyFadedEdges = [...result.nextFadedEdges];
      v.fadeEdgeOrder = [...result.nextFadeEdgeOrder];
    });
    scheduleViewSave();

    // Emit the full faded-edge set to the host so Go can update its wire flags.
    vscode.postMessage({ type: "fade", edges: [...result.fadedEdges] });
  },
}));
