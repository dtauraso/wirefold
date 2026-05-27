// Fade subsystem: pure functions extracted from store.ts.
// The store action (toggleFade) calls computeToggleFade and applies its result.

import type { RFNode, RFEdge, NodeData, EdgeData } from "../types";
import { computeFade, type FadeEdge } from "./fade";

// ---------------------------------------------------------------------------
// applyFade
// ---------------------------------------------------------------------------

/**
 * Re-derive data.faded on every node/edge from the current fade sets.
 * Called after any rebuild that replaces the node/edge arrays.
 */
export function applyFade(
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
// reconcileFadeOrder
// ---------------------------------------------------------------------------

/**
 * Reconcile the fade-order list against the current faded-edge set:
 * - drop any id no longer faded,
 * - append (in stable `edges` order) any newly-faded id not already present.
 * Result equals the current faded-edge set, ordered oldest → newest.
 */
export function reconcileFadeOrder(
  prevOrder: string[],
  edges: RFEdge<EdgeData>[],
): string[] {
  const fadedNow = new Set(edges.filter((e) => !!e.data?.faded).map((e) => e.id));
  const next = prevOrder.filter((id) => fadedNow.has(id));
  const seen = new Set(next);
  for (const e of edges) {
    if (e.data?.faded && !seen.has(e.id)) {
      next.push(e.id);
      seen.add(e.id);
    }
  }
  return next;
}

// ---------------------------------------------------------------------------
// computeToggleFade
// ---------------------------------------------------------------------------

export interface ToggleFadeInput {
  nodes: RFNode<NodeData>[];
  edges: RFEdge<EdgeData>[];
  directlyFadedNodes: Set<string>;
  directlyFadedEdges: Set<string>;
  fadeEdgeOrder: string[];
}

export interface ToggleFadeResult {
  nextFadedNodes: Set<string>;
  nextFadedEdges: Set<string>;
  nextNodes: RFNode<NodeData>[];
  nextEdges: RFEdge<EdgeData>[];
  nextFadeEdgeOrder: string[];
  /** Edge ids that are NEWLY faded this toggle (for pulse cleanup). */
  newlyFadedEdgeIds: Set<string>;
  /** Full faded-edge set after fixpoint (for host message). */
  fadedEdges: Set<string>;
}

/**
 * Pure computation for toggleFade. Takes current state and target, returns
 * next state slices. Side-effects (set, vscode.postMessage, clearPulse, etc.)
 * remain in the store action.
 */
export function computeToggleFade(
  input: ToggleFadeInput,
  target: { kind: "node" | "edge"; id: string },
): ToggleFadeResult {
  const { nodes, edges, directlyFadedNodes, directlyFadedEdges, fadeEdgeOrder } = input;
  const { kind, id } = target;

  // Clone sets so we don't mutate the stored references.
  const nextFadedNodes = new Set<string>(directlyFadedNodes);
  const nextFadedEdges = new Set<string>(directlyFadedEdges);

  // Toggle direction is driven by the element's VISIBLE faded state
  // (data.faded), which covers both directly- and derived-faded. Keying
  // off membership in the direct sets would let a derived-faded node be
  // re-added to the direct set, locking the fixpoint into re-fading forever.

  if (kind === "node") {
    const node = nodes.find((n) => n.id === id);
    const isFaded = !!node?.data.faded;
    if (isFaded) {
      // Reverse-playback unfade: walk a linear path backward through fade
      // history. Unfade N, then its most-recently-faded still-faded incident
      // edge and far node, and continue from that node, never reusing an edge.
      let current: string | undefined = id;
      const usedEdges = new Set<string>();
      const visitedNodes = new Set<string>();
      while (current && !visitedNodes.has(current)) {
        visitedNodes.add(current);
        nextFadedNodes.delete(current);
        const cur: string = current;
        // Candidate edges: incident to `cur`, currently visibly faded
        // (pre-toggle data.faded), and not yet walked.
        const cand: RFEdge<EdgeData>[] = edges.filter(
          (e) =>
            (e.source === cur || e.target === cur) &&
            !usedEdges.has(e.id) &&
            !!e.data?.faded,
        );
        if (cand.length === 0) break;
        // Most-recently faded among candidates = highest index in fadeEdgeOrder.
        const pick: RFEdge<EdgeData> = cand.reduce((best: RFEdge<EdgeData>, e: RFEdge<EdgeData>) =>
          fadeEdgeOrder.indexOf(e.id) > fadeEdgeOrder.indexOf(best.id) ? e : best,
        );
        usedEdges.add(pick.id);
        nextFadedEdges.delete(pick.id);
        const far: string = pick.source === cur ? pick.target : pick.source;
        nextFadedNodes.delete(far);
        current = far;
      }
    } else {
      nextFadedNodes.add(id);
    }
  } else {
    const edge = edges.find((e) => e.id === id);
    const isFaded = !!edge?.data?.faded;
    if (isFaded) {
      nextFadedEdges.delete(id);
      // Also unfade any nodes this edge connects to, so an auto-faded
      // endpoint doesn't immediately re-fade the edge via Rule 1.
      if (edge) {
        nextFadedNodes.delete(edge.source);
        nextFadedNodes.delete(edge.target);
      }
    } else {
      nextFadedEdges.add(id);
    }
  }

  // Compute which edges were previously unfaded so we can clear stale pulses.
  const prevFadedEdgeIds = new Set(
    edges.filter((e) => !!(e.data?.faded)).map((e) => e.id),
  );

  const { nodes: nextNodes, edges: nextEdges } = applyFade(nodes, edges, nextFadedNodes, nextFadedEdges);

  const newlyFadedEdgeIds = new Set<string>();
  for (const e of nextEdges) {
    if (e.data?.faded && !prevFadedEdgeIds.has(e.id)) {
      newlyFadedEdgeIds.add(e.id);
    }
  }

  // Derive fadedEdges set for the host message.
  const fadedEdges = new Set(nextEdges.filter((e) => !!(e.data?.faded)).map((e) => e.id));

  // Recompute fade order against the newly-derived faded-edge set.
  const nextFadeEdgeOrder = reconcileFadeOrder(fadeEdgeOrder, nextEdges);

  return {
    nextFadedNodes,
    nextFadedEdges,
    nextNodes,
    nextEdges,
    nextFadeEdgeOrder,
    newlyFadedEdgeIds,
    fadedEdges,
  };
}
