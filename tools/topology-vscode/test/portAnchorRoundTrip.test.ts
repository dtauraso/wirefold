// Port anchorId persistence: a Port.anchorId set on a node's input/output must
// survive parseSpec (validator). (The port-anchor DRAG is no longer an editor→Go edit
// message — it is produced in-process by the gesture FSM from raw-input — so there is no
// port-anchor bridge record to round-trip here any more.)

import { describe, it, expect } from "vitest";
import { parseSpec } from "../src/schema/parse-spec";
import type { Spec } from "../src/schema/types-graph";

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
