// Tier 2 retro: id rename atomicity. Locks down the already-shipped feature
// against partial-overlap regressions — every spot that holds a node id
// (edges, timing.fires, timing.state keys, sidecar nodeIds / memberIds /
// lastSelectionIds) must be rewritten in lockstep.

import { describe, expect, it } from "vitest";
import { type Spec } from "../src/schema";
import { applyRename } from "../src/webview/state/ops/rename";
import { type ViewerState } from "../src/webview/state/viewer/types";

function fixture(): { spec: Spec; vs: ViewerState } {
  const spec: Spec = {
    nodes: [
      { id: "old", type: "Generic", x: 0, y: 0 },
      { id: "other", type: "Generic", x: 50, y: 0 },
    ],
    edges: [
      { id: "e1", source: "old", sourceHandle: "out", target: "other", targetHandle: "in", kind: "chain" },
      { id: "e2", source: "other", sourceHandle: "out", target: "old", targetHandle: "in", kind: "chain" },
    ],
  };
  const vs: ViewerState = {
    views: [{ name: "v", viewport: { x: 0, y: 0, w: 100, h: 100 }, nodeIds: ["old", "other"] }],
    folds: [{ id: "f", label: "F", memberIds: ["old", "other"], position: [0, 0], collapsed: false }],
    lastSelectionIds: ["old"],
  };
  return { spec, vs };
}

describe("applyRename atomicity", () => {
  type Site = { name: string; pull: (s: Spec, v: ViewerState) => unknown[] };
  const sites: Site[] = [
    { name: "edge.source", pull: (s) => s.edges.map((e) => e.source) },
    { name: "edge.target", pull: (s) => s.edges.map((e) => e.target) },
    { name: "view.nodeIds", pull: (_, v) => v.views!.flatMap((x) => x.nodeIds) },
    { name: "fold.memberIds", pull: (_, v) => v.folds!.flatMap((x) => x.memberIds) },
    { name: "lastSelectionIds", pull: (_, v) => v.lastSelectionIds! },
  ];

  for (const site of sites) {
    it(`rewrites ${site.name}`, () => {
      const { spec, vs } = fixture();
      expect(applyRename(spec, vs, "old", "renamed")).toBeNull();
      const flat = site.pull(spec, vs);
      expect(flat).not.toContain("old");
      expect(flat).toContain("renamed");
    });
  }

  it("leaves unrelated ids untouched", () => {
    const { spec, vs } = fixture();
    applyRename(spec, vs, "old", "renamed");
    expect(spec.nodes.find((n) => n.id === "other")).toBeTruthy();
    expect(vs.views![0].nodeIds).toContain("other");
  });

  it("rejects invalid Go identifiers", () => {
    const { spec, vs } = fixture();
    expect(applyRename(spec, vs, "old", "1bad")).toMatch(/not a valid Go identifier/);
    // Spec must not be mutated on rejection.
    expect(spec.nodes[0].id).toBe("old");
  });

  it("rejects clashing ids", () => {
    const { spec, vs } = fixture();
    expect(applyRename(spec, vs, "old", "other")).toMatch(/already exists/);
    expect(spec.nodes[0].id).toBe("old");
  });

});
