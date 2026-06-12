// Phase 1 port-anchor persistence: a Port.anchor set on a node's input/output must
// survive parseSpec (validator), specToFlow → flowToSpec (save/load adapter round-trip),
// and the messages.ts parseEdit validator for the "port-anchor" edit op.

import { describe, it, expect } from "vitest";
import { parseSpec } from "../src/schema";
import { specToFlow } from "../src/webview/state/adapter/spec-to-flow";
import { flowToSpec } from "../src/webview/state/adapter/flow-to-spec";
import { parseWebviewToHost } from "../src/messages";
import type { Spec } from "../src/schema";

const anchor = { x: 0, y: 1, z: 0 }; // top of the ring

const specWithAnchor = {
  nodes: [
    {
      id: "n",
      type: "Generic",
      inputs: [{ name: "in", kind: "chain", anchor }],
      outputs: [{ name: "out", kind: "chain" }],
    },
  ],
  edges: [],
};

describe("Port.anchor persistence (phase 1)", () => {
  it("parseSpec accepts and preserves anchor", () => {
    const spec = parseSpec(specWithAnchor) as Spec;
    expect(spec.nodes[0].inputs?.[0].anchor).toEqual(anchor);
  });

  it("survives specToFlow → flowToSpec round-trip", () => {
    const spec = parseSpec(specWithAnchor) as Spec;
    const { nodes, edges } = specToFlow(spec, {});
    const back = flowToSpec(nodes, edges, spec);
    expect(back.nodes[0].inputs?.[0].anchor).toEqual(anchor);
  });

  it("parseWebviewToHost validates the port-anchor edit op", () => {
    const msg = {
      type: "edit",
      op: "port-anchor",
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
      op: "port-anchor",
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
      op: "port-anchor",
      node: "n",
      port: "in",
      isInput: true,
      anchor,
      keys: [],
    };
    expect(parseWebviewToHost(msg)).toBeUndefined();
  });
});
