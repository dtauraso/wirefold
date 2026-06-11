// Unit tests for structuralKey — the structural fingerprint that decides
// whether an external topology.json change requires a graph rebuild + Go
// restart (structural) or can be ignored to avoid jolting the animation
// (view-only).

import { describe, expect, it } from "vitest";
import { structuralKey } from "../src/extension/structural-key";

describe("structuralKey", () => {
  it("same nodes/edges, different view → equal keys", () => {
    const a = JSON.stringify({
      nodes: [{ id: "n1" }, { id: "n2" }],
      edges: [{ id: "e1", source: "n1", target: "n2" }],
      view: { nodes: { n1: { x: 0, y: 0 } }, fades: [] },
    });
    const b = JSON.stringify({
      nodes: [{ id: "n1" }, { id: "n2" }],
      edges: [{ id: "e1", source: "n1", target: "n2" }],
      view: { nodes: { n1: { x: 999, y: 42 } }, fades: ["n1"] },
    });
    expect(structuralKey(a)).toBe(structuralKey(b));
  });

  it("incidental key order does not change the key", () => {
    const a = JSON.stringify({ nodes: [{ id: "n1", kind: "And" }], edges: [] });
    const b = JSON.stringify({ edges: [], nodes: [{ kind: "And", id: "n1" }] });
    expect(structuralKey(a)).toBe(structuralKey(b));
  });

  it("different nodes/edges → different keys", () => {
    const a = JSON.stringify({ nodes: [{ id: "n1" }], edges: [], view: {} });
    const b = JSON.stringify({ nodes: [{ id: "n1" }, { id: "n2" }], edges: [], view: {} });
    expect(structuralKey(a)).not.toBe(structuralKey(b));
  });

  it("malformed JSON → falls back to raw text", () => {
    const bad = "{ this is not json";
    expect(structuralKey(bad)).toBe(bad);
  });
});
