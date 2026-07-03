// Port anchorId persistence: a Port.anchorId set on a node's input/output must
// survive parseSpec (validator). Also validates the messages.ts port-anchor edit
// (op="update", kind="node", attr="anchor" — IPC message for dragging port
// positions) is unaffected.

import { describe, it, expect } from "vitest";
import { parseSpec } from "../src/schema";
import { encodeEditUpdate, decodeInputRecord } from "../src/schema/input-layout";
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

describe("port-anchor binary edit (op=update kind=node attr=anchor)", () => {
  // The port-anchor edit is now encoded as a BINARY edit-update record: entity-kind byte
  // "node" + a JSON payload leaf carrying the structural fields (node/port/isInput/anchor/
  // keys). Round-trip it through encode/decode and assert the entity + payload survive
  // (Go decodes the same record via input_codec.go and mail-sorts by keys).
  it("round-trips the port-anchor edit through the binary codec", () => {
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
    const decoded = decodeInputRecord(encodeEditUpdate("node", msg));
    expect(decoded).toEqual({ kind: "edit-update", entity: "node", payload: msg });
  });
});
