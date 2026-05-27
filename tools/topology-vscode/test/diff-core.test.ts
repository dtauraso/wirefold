// Tier 2 contract test for diffSpecs. Locks the diff shape down before the
// renderer-side decoration adds noise. See phase-5.md.

import { describe, expect, it } from "vitest";
import { parseSpec, type Spec } from "../src/schema";
import { diffSpecs } from "../src/webview/state/ops/diff";

function spec(s: Partial<Spec> & { nodes: Spec["nodes"]; edges: Spec["edges"] }): Spec {
  return parseSpec({ nodes: s.nodes, edges: s.edges });
}

const baseA = (): Spec =>
  spec({
    nodes: [
      { id: "a", type: "Generic", x: 0, y: 0 },
      { id: "b", type: "Generic", x: 100, y: 0 },
    ],
    edges: [
      { id: "aToB", source: "a", sourceHandle: "", target: "b", targetHandle: "", kind: "any" },
    ],
  });

describe("diffSpecs — purity & determinism", () => {
  it("does not mutate either input", () => {
    const a = baseA();
    const b = spec({
      nodes: [...a.nodes, { id: "c", type: "Generic", x: 200, y: 0 }],
      edges: a.edges,
    });
    const aSnap = JSON.stringify(a);
    const bSnap = JSON.stringify(b);
    diffSpecs(a, b);
    expect(JSON.stringify(a)).toBe(aSnap);
    expect(JSON.stringify(b)).toBe(bSnap);
  });

  it("is deterministic across calls", () => {
    const a = baseA();
    const b = spec({
      nodes: [
        { id: "b", type: "Generic", x: 100, y: 0 },
        { id: "c", type: "Generic", x: 200, y: 0 },
      ],
      edges: [],
    });
    const d1 = diffSpecs(a, b);
    const d2 = diffSpecs(a, b);
    expect(d1).toEqual(d2);
  });
});

describe("diffSpecs — node categories", () => {
  it("reports added and removed nodes", () => {
    const a = baseA();
    const b = spec({
      nodes: [
        { id: "b", type: "Generic", x: 100, y: 0 },
        { id: "c", type: "Generic", x: 200, y: 0 },
      ],
      edges: [],
    });
    const d = diffSpecs(a, b);
    expect(d.nodes.added).toEqual(["c"]);
    expect(d.nodes.removed).toEqual(["a", "aToB" as never].filter((x) => x === "a"));
    expect(d.nodes.moved).toEqual([]);
  });

  it("moved is always empty (positions live in view, not spec)", () => {
    // Positions moved to topology.view.json in audit #15.
    // spec-level diff has no position information, so moved is always [].
    const a = baseA();
    const d = diffSpecs(a, a);
    expect(d.nodes.moved).toEqual([]);
  });
});

describe("diffSpecs — edge categories", () => {
  it("reports added and removed edges", () => {
    const a = baseA();
    const b = spec({
      nodes: a.nodes,
      edges: [
        { id: "aToBalt", source: "a", sourceHandle: "", target: "b", targetHandle: "", kind: "any" },
      ],
    });
    const d = diffSpecs(a, b);
    expect(d.edges.added).toEqual(["aToBalt"]);
    expect(d.edges.removed).toEqual(["aToB"]);
    expect(d.edges.rewired).toEqual([]);
  });

  it("reports rewired when endpoints change but id is preserved", () => {
    const a = baseA();
    const b = spec({
      nodes: [...a.nodes, { id: "c", type: "Generic", x: 200, y: 0 }],
      edges: [
        { id: "aToB", source: "a", sourceHandle: "", target: "c", targetHandle: "", kind: "any" },
      ],
    });
    const d = diffSpecs(a, b);
    expect(d.edges.rewired).toEqual(["aToB"]);
    expect(d.edges.added).toEqual([]);
    expect(d.edges.removed).toEqual([]);
  });
});

describe("diffSpecs — argument-swap symmetry", () => {
  it("swaps added and removed when args swap", () => {
    const a = baseA();
    const b = spec({
      nodes: [
        { id: "b", type: "Generic", x: 100, y: 0 },
        { id: "c", type: "Generic", x: 200, y: 0 },
      ],
      edges: [
        { id: "bToC", source: "b", sourceHandle: "", target: "c", targetHandle: "", kind: "any" },
      ],
    });
    const ab = diffSpecs(a, b);
    const ba = diffSpecs(b, a);
    expect(ba.nodes.added).toEqual(ab.nodes.removed);
    expect(ba.nodes.removed).toEqual(ab.nodes.added);
    expect(ba.edges.added).toEqual(ab.edges.removed);
    expect(ba.edges.removed).toEqual(ab.edges.added);
  });

  it("keeps moved/rewired sets stable under arg swap", () => {
    const a = baseA();
    const b = spec({
      nodes: [
        { id: "a", type: "Generic", x: 0, y: 0 },
        { id: "b", type: "Generic", x: 200, y: 50 },
      ],
      edges: [
        { id: "aToB", source: "b", sourceHandle: "", target: "a", targetHandle: "", kind: "any" },
      ],
    });
    const ab = diffSpecs(a, b);
    const ba = diffSpecs(b, a);
    expect(ab.nodes.moved).toEqual(ba.nodes.moved);
    expect(ab.edges.rewired).toEqual(ba.edges.rewired);
  });
});
