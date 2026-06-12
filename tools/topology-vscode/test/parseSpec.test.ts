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

describe("parseSpec accepts the Go tree-loader emission shape", () => {
  // Shape captured from `go run . -topology topology` (EmitSpecLine): ports
  // omit `kind` (vestigial, not stored on disk), edges carry both `id` and
  // `label`, and positions live only in view.nodes (no inline capital-P
  // "Position"). Regression for the silent store.load() throw that produced a
  // blank diagram.
  const goEmission = {
    kind: "spec",
    nodes: [
      {
        id: "1", type: "Input",
        data: { init: [1, 0], repeat: true },
        inputs: [{ name: "FeedbackIn", side: "bottom", slot: 1 }],
        outputs: [{ name: "ToReadGate", side: "right", slot: 1 }],
      },
      {
        id: "2", type: "ChainInhibitor",
        data: { state: { held: -1 }, sendRules: { ToNext1: "fireAndForget" } },
        inputs: [{ name: "FromPrevChainInhibitorNode", side: "left", slot: 1 }],
        outputs: [
          { name: "FeedbackOut", side: "top", slot: 1 },
          { name: "ToNext0", side: "bottom", slot: 1 },
          { name: "ToNext1", side: "left", slot: 2 },
        ],
      },
      {
        id: "3", type: "ChainInhibitor",
        data: { state: { held: 0 } },
        inputs: [{ name: "FromPrevChainInhibitorNode", side: "left", slot: 1 }],
        outputs: [{ name: "FeedbackOut", side: "top", slot: 1 }],
      },
    ],
    edges: [
      { id: "1To2", label: "1To2", kind: "chain", source: "1", sourceHandle: "ToReadGate", target: "2", targetHandle: "FromPrevChainInhibitorNode" },
      { id: "2FeedbackTo1", label: "2FeedbackTo1", kind: "chain", source: "2", sourceHandle: "FeedbackOut", target: "1", targetHandle: "FeedbackIn" },
      { id: "2To3", label: "2To3", kind: "chain", source: "2", sourceHandle: "ToNext0", target: "3", targetHandle: "FromPrevChainInhibitorNode" },
    ],
    view: {
      nodes: {
        "1": { x: 21.6, y: 316.1, z: 0 },
        "2": { x: 170.3, y: 353.6, z: 0 },
        "3": { x: 300.0, y: 400.0, z: 0 },
      },
    },
  };

  it("parses to 3 nodes / 3 edges with kind defaulted on ports", () => {
    const s = parseSpec(goEmission);
    expect(s.nodes).toHaveLength(3);
    expect(s.edges).toHaveLength(3);
    // port kind defaulted (omitted by Go), still a valid EdgeKind
    expect(s.nodes[0].inputs?.[0].kind).toBe("signal");
    // edge id preserved from emission
    expect(s.edges.map((e) => e.id)).toEqual(["1To2", "2FeedbackTo1", "2To3"]);
  });

  it("tolerates an inline capital-P Position field if Go ever re-leaks it", () => {
    const withLeak = {
      ...goEmission,
      nodes: goEmission.nodes.map((n) => ({ ...n, Position: { x: 1, y: 2, z: 0 } })),
    };
    expect(() => parseSpec(withLeak)).not.toThrow();
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
