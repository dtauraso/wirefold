// Cover the reject paths in parseSpec / validatePorts. The accept path is
// already exercised by round-trip.test.ts; here we only assert that
// malformed input throws so a corrupt topology.json can't slip through to
// codegen.

import { describe, it, expect } from "vitest";
import { parseSpec } from "../src/schema";

// Generic with explicit port overrides: no required inputs, so tests that omit
// edges remain valid. Port names match okEdge handles.
const okNode = {
  id: "n", type: "Generic",
  outputs: [{ name: "out", kind: "chain" }],
  inputs: [{ name: "in", kind: "chain" }],
};
const okEdge = {
  id: "e", label: "n→n", source: "n", sourceHandle: "out",
  target: "n", targetHandle: "in", kind: "chain",
};

describe("parseSpec rejects", () => {
  it("non-object input", () => {
    expect(() => parseSpec(null)).toThrow();
    expect(() => parseSpec(42)).toThrow();
    expect(() => parseSpec([])).toThrow();
  });

  it("missing or non-array nodes / edges", () => {
    expect(() => parseSpec({ edges: [] })).toThrow();
    expect(() => parseSpec({ nodes: [], edges: "no" })).toThrow();
    expect(() => parseSpec({ nodes: {}, edges: [] })).toThrow();
  });

  it("node with non-string id", () => {
    expect(() =>
      parseSpec({ nodes: [{ ...okNode, id: 1 }], edges: [] }),
    ).toThrow(/spec\.nodes\[0\]\.id/);
  });

  it("edge with kind outside EDGE_KINDS", () => {
    expect(() =>
      parseSpec({
        nodes: [okNode],
        edges: [{ ...okEdge, kind: "weird" }],
      }),
    ).toThrow(/spec\.edges\[0\]\.kind/);
  });

  it("edge with non-string source/target", () => {
    expect(() =>
      parseSpec({
        nodes: [okNode],
        edges: [{ ...okEdge, source: 1 }],
      }),
    ).toThrow(/spec\.edges\[0\]\.source/);
  });

  it("validatePorts: edge points at unknown node", () => {
    expect(() =>
      parseSpec({
        nodes: [okNode],
        edges: [{ ...okEdge, source: "ghost" }],
      }),
    ).toThrow(/unknown source ghost/);
  });

  it("validatePorts: edge handle that does not exist on the node type", () => {
    expect(() =>
      parseSpec({
        nodes: [okNode],
        edges: [{ ...okEdge, sourceHandle: "nope" }],
      }),
    ).toThrow(/has no output port "nope"/);
  });

  it("silently drops legacy timing.steps field (Phase 5.5)", () => {
    // Older topology.json files with the SVG-era master script. Parse
    // succeeds and the field is gone from the resulting spec.
    const s = parseSpec({
      nodes: [okNode], edges: [],
      timing: { steps: [{ t: 0, event: "x", fires: ["n"] }] },
    });
    expect((s.timing as { steps?: unknown }).steps).toBeUndefined();
  });

it("legend row with bad kind", () => {
    expect(() =>
      parseSpec({
        nodes: [okNode], edges: [],
        legend: [{ kind: "weird", name: "n", desc: "d" }],
      }),
    ).toThrow(/spec\.legend\[0\]\.kind/);
  });

  it("notes entry without text", () => {
    expect(() =>
      parseSpec({
        nodes: [okNode], edges: [],
        notes: [{ x: 0, y: 0 }],
      }),
    ).toThrow(/spec\.notes\[0\]\.text/);
  });
});

describe("parseSpec view orphan-edge-key detection", () => {
  it("throws when view.edges contains a key not in spec edges", () => {
    expect(() =>
      parseSpec(
        { nodes: [okNode], edges: [okEdge] },
        { edges: { "ghost-edge": {} } },
      ),
    ).toThrow(/view edge key "ghost-edge" has no matching edge in spec/);
  });

  it("accepts when view.edges keys match spec edge ids", () => {
    expect(() =>
      parseSpec(
        { nodes: [okNode], edges: [okEdge] },
        { edges: { [okEdge.id]: { route: "snake-v" } } },
      ),
    ).not.toThrow();
  });

  it("accepts when view has no edges key", () => {
    expect(() =>
      parseSpec({ nodes: [okNode], edges: [okEdge] }, {}),
    ).not.toThrow();
  });

  it("accepts when view argument is omitted", () => {
    expect(() =>
      parseSpec({ nodes: [okNode], edges: [okEdge] }),
    ).not.toThrow();
  });
});

describe("parseSpec accepts", () => {
  it("a minimal valid spec", () => {
    const s = parseSpec({ nodes: [okNode], edges: [] });
    expect(s.nodes).toHaveLength(1);
    expect(s.edges).toHaveLength(0);
  });

  it("preserves node.props when present", () => {
    const s = parseSpec({
      nodes: [{ ...okNode, props: { delay: 2, label: "x" } }],
      edges: [],
    });
    expect(s.nodes[0].props).toEqual({ delay: 2, label: "x" });
  });

  it("rejects non-scalar values inside node.props", () => {
    expect(() =>
      parseSpec({
        nodes: [{ ...okNode, props: { delay: { nested: 1 } } }],
        edges: [],
      }),
    ).toThrow(/spec\.nodes\[0\]\.props\.delay/);
  });
});
