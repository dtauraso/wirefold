// Locks deterministic serialization of view.nodes key order. The editor used
// to write topology.json#view with node keys in incidental in-memory insertion
// order (parse order + node-move reinsertion), so a load→(no change)→save
// produced a no-op key-churn diff. serializeViewerState now emits node keys
// sorted by id, making the bytes a pure function of the data.

import { describe, it, expect } from "vitest";
import {
  parseViewerState,
  serializeViewerState,
  type ViewerState,
} from "../src/webview/state/viewer/types";

// Node keys deliberately out of sorted order, including inactive-style ids.
const FIXTURE = JSON.stringify({
  nodes: {
    windowAndGate1: { x: 3, y: 3 },
    bootstrap_rg: { x: 1, y: 1 },
    inhibitRight0: { x: 2, y: 2 },
    aNode: { x: 0, y: 0 },
  },
  directlyFadedNodes: ["inhibitRight0", "aNode"],
  fadeEdgeOrder: ["e2", "e1", "e3"], // order-meaningful: must NOT be sorted
});

function nodeKeyOrder(text: string): string[] {
  return Object.keys((JSON.parse(text) as ViewerState).nodes ?? {});
}

describe("serializeViewerState — stable view.nodes order", () => {
  it("emits node keys sorted by id regardless of insertion order", () => {
    const out = serializeViewerState(parseViewerState(FIXTURE));
    expect(nodeKeyOrder(out)).toEqual(["aNode", "bootstrap_rg", "inhibitRight0", "windowAndGate1"]);
  });

  it("is idempotent: a second parse→serialize is byte-identical", () => {
    const once = serializeViewerState(parseViewerState(FIXTURE));
    const twice = serializeViewerState(parseViewerState(once));
    expect(twice).toBe(once);
  });

  it("does not reorder fadeEdgeOrder (order is semantically meaningful)", () => {
    const out = serializeViewerState(parseViewerState(FIXTURE));
    expect((JSON.parse(out) as ViewerState).fadeEdgeOrder).toEqual(["e2", "e1", "e3"]);
  });

  it("a single node-position update keeps all other keys in sorted position", () => {
    const state = parseViewerState(FIXTURE);
    // Simulate the node-move patch: reassign one node's key (this is what moved
    // the key to the end in the in-memory object and caused the churn).
    state.nodes!["bootstrap_rg"] = { x: 99, y: 99 };
    const out = serializeViewerState(state);
    expect(nodeKeyOrder(out)).toEqual(["aNode", "bootstrap_rg", "inhibitRight0", "windowAndGate1"]);
    expect((JSON.parse(out) as ViewerState).nodes!["bootstrap_rg"]).toEqual({ x: 99, y: 99 });
  });
});
