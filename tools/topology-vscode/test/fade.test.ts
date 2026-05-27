// Unit tests for computeFade fixpoint propagation.

import { describe, expect, it } from "vitest";
import { computeFade, type FadeEdge } from "../src/webview/three/fade";

// Helper: build a minimal graph
function graph(
  nodeIds: string[],
  edges: { id: string; source: string; target: string }[],
) {
  return { nodeIds, edges: edges as FadeEdge[] };
}

describe("computeFade — fixpoint propagation", () => {
  it("directly-faded node cascades to its incident edges", () => {
    const g = graph(["A", "B"], [{ id: "e1", source: "A", target: "B" }]);
    const { fadedNodes, fadedEdges } = computeFade(
      g.nodeIds,
      g.edges,
      new Set(["A"]),
      new Set(),
    );
    expect(fadedNodes.has("A")).toBe(true);
    expect(fadedEdges.has("e1")).toBe(true);
  });

  it("auto-fades node B when its last live edge is faded (cascade from A)", () => {
    // A and B connected by e1. Fading A fades e1 → B has no non-faded edges → B auto-fades.
    const g = graph(["A", "B"], [{ id: "e1", source: "A", target: "B" }]);
    const { fadedNodes } = computeFade(
      g.nodeIds,
      g.edges,
      new Set(["A"]),
      new Set(),
    );
    expect(fadedNodes.has("B")).toBe(true);
  });

  it("node with one live edge is NOT auto-faded", () => {
    // A-B via e1, A-C via e2. Fade e1 only. B has no live edges → auto-fades.
    // C still has e2 live → C stays unfaded.
    const g = graph(
      ["A", "B", "C"],
      [
        { id: "e1", source: "A", target: "B" },
        { id: "e2", source: "A", target: "C" },
      ],
    );
    const { fadedNodes } = computeFade(
      g.nodeIds,
      g.edges,
      new Set(),
      new Set(["e1"]),
    );
    expect(fadedNodes.has("B")).toBe(true);   // all of B's edges faded
    expect(fadedNodes.has("C")).toBe(false);  // C still has e2
    expect(fadedNodes.has("A")).toBe(false);  // A still has e2
  });

  it("node with no edges is only faded when directly faded", () => {
    const g = graph(["lonely"], []);
    const { fadedNodes: fn1 } = computeFade(g.nodeIds, g.edges, new Set(), new Set());
    expect(fn1.has("lonely")).toBe(false);

    const { fadedNodes: fn2 } = computeFade(g.nodeIds, g.edges, new Set(["lonely"]), new Set());
    expect(fn2.has("lonely")).toBe(true);
  });

  it("unfade: removing a node from directly-faded restores it and its edges", () => {
    const g = graph(["A", "B"], [{ id: "e1", source: "A", target: "B" }]);
    // Start faded, then "unfade" A by passing empty directly-faded sets.
    const { fadedNodes, fadedEdges } = computeFade(
      g.nodeIds,
      g.edges,
      new Set(),   // A no longer directly faded
      new Set(),
    );
    expect(fadedNodes.size).toBe(0);
    expect(fadedEdges.size).toBe(0);
  });

  it("cascade propagates across multiple hops to fixpoint when both ends are faded", () => {
    // Chain: A → B → C via e1, e2.
    // Fade both A and C directly. A fades e1; C fades e2.
    // B now has ALL incident edges faded (e1, e2) → B auto-fades.
    const g = graph(
      ["A", "B", "C"],
      [
        { id: "e1", source: "A", target: "B" },
        { id: "e2", source: "B", target: "C" },
      ],
    );
    const { fadedNodes, fadedEdges } = computeFade(
      g.nodeIds,
      g.edges,
      new Set(["A", "C"]),
      new Set(),
    );
    expect(fadedNodes.has("A")).toBe(true);
    expect(fadedNodes.has("C")).toBe(true);
    expect(fadedNodes.has("B")).toBe(true);   // auto-faded: all incident edges faded
    expect(fadedEdges.has("e1")).toBe(true);
    expect(fadedEdges.has("e2")).toBe(true);
  });
});
