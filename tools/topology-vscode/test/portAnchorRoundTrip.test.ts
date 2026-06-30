// Port anchorId persistence: a Port.anchorId set on a node's input/output must
// survive parseSpec (validator). Also validates the messages.ts port-anchor edit
// (op="update", kind="node", attr="anchor" — IPC message for dragging port
// positions) is unaffected.

import { describe, it, expect } from "vitest";
import { parseSpec } from "../src/schema";
import { parseWebviewToHost } from "../src/messages";
import type { Spec } from "../src/schema";

const anchor = { x: 0, y: 1, z: 0 }; // used by the IPC edit op (not Port schema)

const specWithAnchorId = {
  nodes: [
    {
      id: "n",
      type: "Generic",
      inputs: [{ name: "in", kind: "chain", anchorId: 3 }],
      outputs: [{ name: "out", kind: "chain" }],
    },
  ],
  edges: [],
};

describe("Port.anchorId persistence", () => {
  it("parseSpec accepts and preserves anchorId", () => {
    const spec = parseSpec(specWithAnchorId) as Spec;
    expect(spec.nodes[0].inputs?.[0].anchorId).toEqual(3);
  });

  it("parseSpec accepts port without anchorId (defaults absent)", () => {
    const spec = parseSpec(specWithAnchorId) as Spec;
    expect(spec.nodes[0].outputs?.[0].anchorId).toBeUndefined();
  });

  it("parseSpec rejects negative anchorId", () => {
    const specBad = {
      nodes: [
        {
          id: "n",
          type: "Generic",
          inputs: [{ name: "in", kind: "chain", anchorId: -1 }],
          outputs: [],
        },
      ],
      edges: [],
    };
    expect(() => parseSpec(specBad)).toThrow(/anchorId.*non-negative integer/);
  });
});

describe("port-anchor IPC edit (op=update kind=node attr=anchor)", () => {
  it("parseWebviewToHost validates the port-anchor edit", () => {
    const msg = {
      type: "edit",
      op: "update",
      kind: "node",
      attr: "anchor",
      node: "n",
      port: "in",
      isInput: true,
      anchor,
      keys: ["n", "n→n"],
    };
    expect(parseWebviewToHost(msg)).toEqual(msg);
  });

  it("rejects a port-anchor edit with a malformed anchor", () => {
    const msg = {
      type: "edit",
      op: "update",
      kind: "node",
      attr: "anchor",
      node: "n",
      port: "in",
      isInput: true,
      anchor: { x: 0, y: 1 }, // missing z
      keys: ["n"],
    };
    expect(parseWebviewToHost(msg)).toBeUndefined();
  });

  it("rejects a port-anchor edit with empty keys", () => {
    const msg = {
      type: "edit",
      op: "update",
      kind: "node",
      attr: "anchor",
      node: "n",
      port: "in",
      isInput: true,
      anchor,
      keys: [],
    };
    expect(parseWebviewToHost(msg)).toBeUndefined();
  });
});
