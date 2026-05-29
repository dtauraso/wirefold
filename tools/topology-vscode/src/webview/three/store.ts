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
import { NODE_DEFS } from "../../schema/node-defs";
import { PSEUDO_KIND_PREFIX, type PseudoKind } from "../../messages";
import { clearPulse, getPulseMap, patchPulse, setCurve } from "./pulse-state";
import { rfArcLength, arcLengthToSimLatencyMs, buildEdgeCurve } from "./geometry-helpers";
import { getPauseAdjustedNow } from "../state/run-status";
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
      // Preserve previously-rendered pseudocode across reloads. The async
      // `*-render-result` messages patch `n.data.pseudo` after load(); a second
      // load() would otherwise wipe it because specToFlow rebuilds nodes
      // without a `pseudo` field.
      const prevPseudo = new Map(
        get().nodes.map((n) => [n.id, (n.data as { pseudo?: string } | undefined)?.pseudo]),
      );
      const flow = specToFlow(spec, next, next.lastSelectionIds ?? []);
      let nodes = flow.nodes as RFNode<NodeData>[];
      let edges = flow.edges as RFEdge<EdgeData>[];
      nodes = nodes.map((n) => {
        const p = prevPseudo.get(n.id);
        return p ? { ...n, data: { ...n.data, pseudo: p } } : n;
      });
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
      // Populate curve store synchronously after load so PulseBead can read
      // curves before the first React commit completes.
      const nodeMapForLoad = new Map(nodes.map((n) => [n.id, n]));
      for (const edge of edges) {
        const s = nodeMapForLoad.get(edge.source);
        const t = nodeMapForLoad.get(edge.target);
        if (s && t) setCurve(edge.id, buildEdgeCurve(s, t));
      }
      postLog("lifecycle", { phase: "store:load", nodes: nodes.length, edges: edges.length });

      // Fire per-node pseudocode render requests for kinds with hasPseudo.
      // Responses arrive asynchronously and patch n.data.pseudo via the host
      // message listener in main.tsx (no regen on edit; reload refreshes).
      for (const n of nodes) {
        const kind = n.data?.type;
        if (!kind || !NODE_DEFS[kind]?.hasPseudo) continue;
        if (!(kind in PSEUDO_KIND_PREFIX)) continue;
        const prefix = PSEUDO_KIND_PREFIX[kind as PseudoKind];
        vscode.postMessage({ type: `${prefix}-render`, nodeId: n.id });
      }
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
    // Populate curve store for the new edge synchronously.
    const srcNode = nodes.find((n) => n.id === sourceId);
    const tgtNode = nodes.find((n) => n.id === targetId);
    if (srcNode && tgtNode) setCurve(result.id, buildEdgeCurve(srcNode, tgtNode));
    scheduleSave();
    return result.id;
  },

  moveNode(id, x, y) {
    const { nodes, edges } = get();
    const nextNodes = nodes.map((n) =>
      n.id === id ? { ...n, position: { x, y } } : n,
    );
    set({ nodes: nextNodes });

    // TS-local latency recompute: patch in-flight pulses on affected edges so
    // bead speed stays at the uniform 0.08 wu/ms target during drag.
    // Curve store is updated for ALL touching edges (not just in-flight) so the
    // next pulse always reads the correct geometry without a React-commit lag.
    const pulseMap = getPulseMap();
    const now = getPauseAdjustedNow();
    for (const edge of edges) {
      if (edge.source !== id && edge.target !== id) continue;
      // Use the NEW position for the dragged node, current position for the other.
      const srcNode = nextNodes.find((n) => n.id === edge.source);
      const tgtNode = nextNodes.find((n) => n.id === edge.target);
      if (!srcNode || !tgtNode) continue;
      // Always update curve store synchronously so PulseBead reads the new geometry
      // in the same useFrame tick without waiting for a React commit.
      setCurve(edge.id, buildEdgeCurve(srcNode, tgtNode));
      const pulse = pulseMap.get(edge.id);
      if (!pulse) continue;
      const newArcLength = rfArcLength(
        srcNode.position.x, srcNode.position.y,
        tgtNode.position.x, tgtNode.position.y,
      );
      const newSimLatencyMs = arcLengthToSimLatencyMs(newArcLength);
      // Preserve fractional progress t_curr so the bead doesn't jump.
      const elapsed = now - pulse.startTime;
      const tCurr = Math.min(1, elapsed / pulse.simLatencyMs);
      const newStartTime = now - tCurr * newSimLatencyMs;
      patchPulse(edge.id, newSimLatencyMs, newStartTime);
    }
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
