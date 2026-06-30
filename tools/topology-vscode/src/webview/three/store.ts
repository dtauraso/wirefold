// R3F zustand store — single source of truth for node/edge state.
// save.ts reads useThreeStore.getState().nodes/edges directly (no mirror).

import { create } from "zustand";
import type { RFNode, RFEdge, NodeData, EdgeData } from "../types";
import { parseSpec } from "../../schema";
import { specToFlow } from "../state/adapter/spec-to-flow";
import { viewerState, setViewerState, patchViewerState } from "../state/viewer-state";
import { parseViewerState, mergeSceneIntoViewerState } from "../state/viewer/types";
import { markViewSynced, scheduleViewSave, viewSyncedKey } from "../save";
import { postLog } from "../log/post";
import { vscode } from "../vscode-api";
import { clearPulsesForEdge, clearAllPulses } from "./pulse-state";
import { useEdgeGeometryStore } from "./edge-geometry";
import { applyFade, reconcileFadeOrder, computeToggleFade } from "./fade-actions";
import { buildEdge } from "./edge-creation";

// ---------------------------------------------------------------------------
// State shape
// ---------------------------------------------------------------------------

export interface ThreeStoreState {
  nodes: RFNode<NodeData>[];
  edges: RFEdge<EdgeData>[];
  // Incremented each time content is (re)loaded; used to trigger camera re-fit.
  loadEpoch: number;
  // Non-null when the last load() call threw (parse failure). Cleared on the next
  // successful load. Surfaced as a user-visible overlay so a blank diagram is
  // diagnosable without opening .probe/ts-errors.jsonl.
  loadError: string | null;

  // --- Fade state ---
  directlyFadedNodes: Set<string>;
  directlyFadedEdges: Set<string>;
  // Faded-edge ids in fade order (oldest → newest). Single source of truth
  // for "which edge faded most recently" — drives the reverse-playback walk.
  fadeEdgeOrder: string[];

  // --- Actions ---
  load: (text: string, sceneText?: string) => void;
  setNodes: (updater: RFNode<NodeData>[] | ((ns: RFNode<NodeData>[]) => RFNode<NodeData>[])) => void;
  setEdges: (updater: RFEdge<EdgeData>[] | ((es: RFEdge<EdgeData>[]) => RFEdge<EdgeData>[])) => void;
  createEdge: (
    sourceId: string,
    sourceHandle: string | null,
    targetId: string,
    targetHandle: string | null,
  ) => string | null;
  /** Toggle fade on a node or edge. Recomputes fixpoint and emits updated faded-edge set to host. */
  toggleFade: (target: { kind: "node" | "edge"; id: string }) => void;
  /** Remove an edge by id from the spec and persist. */
  deleteEdge: (id: string) => void;
}

// ---------------------------------------------------------------------------
// Store
// ---------------------------------------------------------------------------

export const useThreeStore = create<ThreeStoreState>((set, get) => ({
  nodes: [],
  edges: [],
  loadEpoch: 0,
  loadError: null,
  directlyFadedNodes: new Set<string>(),
  directlyFadedEdges: new Set<string>(),
  fadeEdgeOrder: [],

  load(text: string, sceneText?: string) {
    try {
      // Run-start boundary: wipe any transient bead left in the store by the prior
      // run's process. Go is killed + re-spawned fresh (zero in-flight beads) every
      // run/restart, but a bead past "send" yet before "arrive" when stop hit never
      // got its clearing "arrive" event — it survives here. This is the ONLY reset:
      // pause does not call load(), so beads correctly persist across pause.
      clearAllPulses();
      const raw = JSON.parse(text);
      const spec = parseSpec(raw);
      // Diagram view: positions + fades from topology.json#view (Go reads view.nodes).
      const viewText = raw.view !== undefined ? JSON.stringify(raw.view) : undefined;
      const diagramView = parseViewerState(viewText);
      // Scene view: camera, camera3d, labelsGlobalHidden from topology.scene.json (optional).
      const sceneView = sceneText !== undefined ? parseViewerState(sceneText) : undefined;
      const next = sceneView !== undefined
        ? mergeSceneIntoViewerState(diagramView, sceneView)
        : diagramView;
      setViewerState(next);
      // Race guard keyed on the combined diagram+scene payload — must match
      // performViewSave's guard key so the initial load doesn't retrigger a save.
      markViewSynced(viewSyncedKey(next));
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
        loadError: null,
        directlyFadedNodes: restoredFadedNodes,
        directlyFadedEdges: restoredFadedEdges,
        fadeEdgeOrder,
      });
      // Phase 3: TS computes NO edge geometry. Go holds node positions + per-edge
      // control points and streams them (geometry trace) on load and on every move;
      // SingleEdgeTube draws the tube from the edge-geometry store. The store no
      // longer builds curves here.
      postLog("lifecycle", { phase: "store:load", nodes: nodes.length, edges: edges.length });
    } catch (err) {
      console.error("[ThreeStore] load failed", err);
      // Permanent diagnostic: surface the throw to .probe/ts-errors.jsonl so a
      // silent parse failure (blank diagram, no store:load) is observable.
      // "load-error" must stay in ERROR_LABELS (extension/webview-log.ts).
      const e = err as { message?: string; stack?: string };
      const message = e?.message ?? String(err);
      postLog("load-error", {
        message,
        stack: e?.stack ?? null,
      });
      set({ loadError: message });
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

  createEdge(sourceId, sourceHandleIn, targetId, targetHandleIn) {
    const { nodes, edges } = get();
    const result = buildEdge(nodes, edges, sourceId, sourceHandleIn, targetId, targetHandleIn);
    if (!result) {
      return null;
    }
    const nextEdges = [...edges, result.newEdge];
    set({ edges: nextEdges });
    // Phase 3: TS computes no geometry. Go is the authoritative curve holder and
    // streams the edge's control points; the tube renders from Go's stream once Go
    // knows the edge (on its next load/run). No TS-built curve here.
    // Tell Go to un-silence this wire so it carries pulses again (mirrors the
    // delete edit). Single geometry-CRUD bridge: edit/create.
    postLog("edit-create-post", { edgeId: result.id, target: result.newEdge.target, targetHandle: result.newEdge.targetHandle ?? "" });
    vscode.postMessage({
      type: "edit",
      op: "create",
      target: result.newEdge.target,
      targetHandle: result.newEdge.targetHandle ?? "",
    });
    return result.id;
  },

  deleteEdge(id) {
    const { edges } = get();
    const edge = edges.find((ed) => ed.id === id);
    // Tell Go to drop this wire's in-flight pulse and free its parked sender,
    // keyed by the destination slot identity (target + targetHandle). Single
    // geometry-CRUD bridge: edit/delete (Go cancels clock-delivery + echoes
    // pulse-cancelled atomically).
    if (edge) {
      postLog("edit-delete-post", { edgeId: id, target: edge.target, targetHandle: edge.targetHandle ?? "", found: true });
      vscode.postMessage({
        type: "edit",
        op: "delete",
        target: edge.target,
        targetHandle: edge.targetHandle ?? "",
      });
    } else {
      postLog("edit-delete-post", { edgeId: id, found: false });
    }
    const nextEdges = edges.filter((ed) => ed.id !== id);
    set({ edges: nextEdges });
    clearPulsesForEdge(id);
    // Drop Go's streamed segment for this edge so no stale tube can draw.
    useEdgeGeometryStore.getState().removeEdgeSegment(id);
  },

  toggleFade(target) {
    const { nodes, edges, directlyFadedNodes, directlyFadedEdges, fadeEdgeOrder } = get();
    const result = computeToggleFade(
      { nodes, edges, directlyFadedNodes, directlyFadedEdges, fadeEdgeOrder },
      target,
    );

    // Clear pulse state for any edge that is NEWLY faded this toggle.
    for (const edgeId of result.newlyFadedEdgeIds) {
      clearPulsesForEdge(edgeId);
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

    // Emit the full desired faded state to the host so each Go wire sets its own flag.
    // Shape: Record<edgeId, boolean> — faded=true for each faded edge, false for each
    // unfaded edge — so Go's per-wire dispatch can set any wire to its desired state.
    // Single geometry-CRUD bridge: edit/fade. Fire-and-forget.
    const edgeFadeMap: Record<string, boolean> = {};
    for (const e of edges) {
      edgeFadeMap[e.id] = result.fadedEdges.has(e.id);
    }
    vscode.postMessage({ type: "edit", op: "update", kind: "edge", attr: "faded", edges: edgeFadeMap });
  },
}));
