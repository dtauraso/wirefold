// Tier 2 retro: fold/unfold bridge behavior. The adapter takes the spec
// plus a viewer-state folds[] list and emits RF nodes/edges that hide
// collapsed members + reroute crossing edges to the placeholder, without
// mutating the spec. Locks down: collapsed members aren't emitted as RF
// nodes; expanded folds emit a frame around their members; edges fully
// inside a collapsed fold are dropped; edges crossing a collapsed boundary
// are rerouted to the placeholder.

import { describe, expect, it } from "vitest";
import type { Spec } from "../src/schema";
import { specToFlow } from "../src/webview/state/adapter/spec-to-flow";
import { createFold, toggleFold } from "../src/webview/state/ops/fold";
import type { ViewerState } from "../src/webview/state/viewer/types";

function makeSpec(): Spec {
  return {
    nodes: [
      { id: "a", type: "Generic" },
      { id: "b", type: "Generic" },
      { id: "c", type: "Generic" },
      { id: "d", type: "Generic" },
    ],
    edges: [
      { id: "e_ab", source: "a", sourceHandle: "", target: "b", targetHandle: "", kind: "any" },
      { id: "e_bc", source: "b", sourceHandle: "", target: "c", targetHandle: "", kind: "any" },
      { id: "e_cd", source: "c", sourceHandle: "", target: "d", targetHandle: "", kind: "any" },
    ],
  };
}

// Node positions live in view, not spec.
const BASE_NODE_VIEWS = {
  a: { x: 0, y: 0 },
  b: { x: 100, y: 0 },
  c: { x: 200, y: 0 },
  d: { x: 300, y: 0 },
};

describe("fold-aware specToFlow", () => {
  it("collapsed fold hides members, drops internal edges, reroutes crossing edges", () => {
    const spec = makeSpec();
    const vs: ViewerState = { nodes: { ...BASE_NODE_VIEWS } };
    const id = createFold(vs, ["b", "c"], [150, 0]);
    expect(id).toBeDefined();
    const flow = specToFlow(spec, vs.folds, vs);

    const ids = flow.nodes.map((n) => n.id).sort();
    // Members b/c hidden; placeholder fold0 present; a/d still rendered.
    expect(ids).toEqual(["a", "d", "fold0"].sort());

    const placeholder = flow.nodes.find((n) => n.id === "fold0")!;
    expect(placeholder.type).toBe("fold");
    expect(placeholder.data?.collapsed).toBe(true);
    expect(placeholder.data?.memberCount).toBe(2);

    // e_bc is internal to the fold → dropped entirely.
    expect(flow.edges.some((e) => e.id === "e_bc")).toBe(false);

    // e_ab targets b (inside fold) → target rerouted to fold0; source intact.
    const eab = flow.edges.find((e) => e.id === "e_ab")!;
    expect(eab.source).toBe("a");
    expect(eab.target).toBe("fold0");

    // e_cd's source c is inside fold → rerouted to fold0; target intact.
    const ecd = flow.edges.find((e) => e.id === "e_cd")!;
    expect(ecd.source).toBe("fold0");
    expect(ecd.target).toBe("d");
  });

  it("expanded fold emits a frame and keeps members + edges intact", () => {
    const spec = makeSpec();
    const vs: ViewerState = { nodes: { ...BASE_NODE_VIEWS } };
    createFold(vs, ["b", "c"], [150, 0]);
    toggleFold(vs, "fold0"); // collapse → expand

    const flow = specToFlow(spec, vs.folds, vs);
    const memberIds = flow.nodes.filter((n) => n.type === "animated").map((n) => n.id).sort();
    expect(memberIds).toEqual(["a", "b", "c", "d"]);

    const frame = flow.nodes.find((n) => n.type === "fold")!;
    expect(frame.data?.collapsed).toBe(false);
    // Frame must encompass member bounding box (b at x=100, c at x=200,
    // both 110px wide by default).
    expect(frame.position.x).toBeLessThan(100);
    expect(frame.data?.width).toBeGreaterThan(200);

    // All edges still rendered with original endpoints.
    const eab = flow.edges.find((e) => e.id === "e_ab")!;
    expect(eab.source).toBe("a");
    expect(eab.target).toBe("b");
    const ecd = flow.edges.find((e) => e.id === "e_cd")!;
    expect(ecd.source).toBe("c");
    expect(ecd.target).toBe("d");
  });

  it("specToFlow does not mutate the spec when folds are applied", () => {
    const spec = makeSpec();
    const before = JSON.stringify(spec);
    const vs: ViewerState = { nodes: { ...BASE_NODE_VIEWS } };
    createFold(vs, ["b", "c"], [150, 0]);
    specToFlow(spec, vs.folds, vs);
    expect(JSON.stringify(spec)).toBe(before);
  });

  it("deleting a collapsed fold restores members and original edges (as if fold never existed)", () => {
    const spec = makeSpec();
    const vs: ViewerState = { nodes: { ...BASE_NODE_VIEWS } };
    createFold(vs, ["b", "c"], [50, 50]);
    // Sanity: fold collapsed → b/c hidden, e_ab/e_cd rerouted, e_bc dropped.
    let flow = specToFlow(spec, vs.folds, vs);
    expect(flow.nodes.some((n) => n.id === "b")).toBe(false);
    expect(flow.nodes.some((n) => n.id === "c")).toBe(false);

    // Mimic onNodesDelete fold-only branch: remove the fold from viewerState.
    vs.folds = (vs.folds ?? []).filter((f) => f.id !== "fold0");

    flow = specToFlow(spec, vs.folds, vs);
    // Fold gone.
    expect(flow.nodes.some((n) => n.type === "fold")).toBe(false);
    // Members back at their original pre-fold positions.
    const b = flow.nodes.find((n) => n.id === "b")!;
    const c = flow.nodes.find((n) => n.id === "c")!;
    expect(b.position).toEqual({ x: 100, y: 0 });
    expect(c.position).toEqual({ x: 200, y: 0 });
    // All three original edges between actual nodes restored.
    const edgeIds = flow.edges.map((e) => e.id).sort();
    expect(edgeIds).toEqual(["e_ab", "e_bc", "e_cd"]);
    const eab = flow.edges.find((e) => e.id === "e_ab")!;
    const ecd = flow.edges.find((e) => e.id === "e_cd")!;
    expect(eab.source).toBe("a");
    expect(eab.target).toBe("b");
    expect(ecd.source).toBe("c");
    expect(ecd.target).toBe("d");
  });

  it("createFold rejects members already inside another fold (no nesting)", () => {
    const vs: ViewerState = {};
    createFold(vs, ["a", "b"], [0, 0]);
    const second = createFold(vs, ["b", "c"], [0, 0]);
    // Filtered to {c}, only 1 member → too few; rejected.
    expect(second).toBeUndefined();
    expect(vs.folds?.length).toBe(1);
  });
});
