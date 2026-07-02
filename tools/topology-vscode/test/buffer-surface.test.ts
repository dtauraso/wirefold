// Unit tests for buffer-surface.ts — pure on-surface selection-set computation.
//
// Mirrors the pre-branch scene-content.tsx { sphereOwners, surfaceIds } behavior over
// buffer node-row adjacency. Graph used throughout (rows are node indices):
//
//   0 → 1        (0 outputs to 1)
//   0 → 2        (0 outputs to 2)
//   3 → 1        (3 also outputs to 1)
//   1 → 4        (1 outputs to 4)
//
// So node 1 sits on the surface of both sphere-0 and sphere-3, and centers its own
// sphere holding node 4.

import { describe, it, expect } from "vitest";
import { surfaceRowSet, type EdgeAdj } from "../src/webview/three/buffer-surface";

const EDGES: EdgeAdj[] = [
  { src: 0, dst: 1 },
  { src: 0, dst: 2 },
  { src: 3, dst: 1 },
  { src: 1, dst: 4 },
];

describe("surfaceRowSet", () => {
  it("returns empty when nothing is selected", () => {
    expect(surfaceRowSet(-1, "surface", EDGES).size).toBe(0);
    expect(surfaceRowSet(-1, "own", EDGES).size).toBe(0);
  });

  it("own mode: owner = selected, surface = selected + its children", () => {
    // Select 0 in own mode → owner 0, children {1,2}.
    const set = surfaceRowSet(0, "own", EDGES);
    expect([...set].sort()).toEqual([0, 1, 2]);
  });

  it("own mode on a childless node highlights only itself", () => {
    // Node 4 has no outgoing edges.
    expect([...surfaceRowSet(4, "own", EDGES)]).toEqual([4]);
  });

  it("surface mode: owners = nodes outputting to selected, surface = owners + their children", () => {
    // Select 1 in surface mode → owners {0,3}; surface = {0,3} ∪ children(0)={1,2}
    //  ∪ children(3)={1} plus the selected node itself → {0,1,2,3}.
    const set = surfaceRowSet(1, "surface", EDGES);
    expect([...set].sort()).toEqual([0, 1, 2, 3]);
  });

  it("surface mode on a node with no incoming edges highlights only itself", () => {
    // Node 0 has no incoming edges → no owners; only the selected node highlights.
    expect([...surfaceRowSet(0, "surface", EDGES)]).toEqual([0]);
  });

  it("ignores edges with an unresolved (-1) endpoint", () => {
    const edges: EdgeAdj[] = [
      { src: 0, dst: 1 },
      { src: -1, dst: 1 }, // unresolved source: must not create a phantom owner
      { src: 0, dst: -1 }, // unresolved dest: must not become a child
    ];
    // Surface-select 1: only owner 0 resolves; its resolved child is 1.
    expect([...surfaceRowSet(1, "surface", edges)].sort()).toEqual([0, 1]);
    // Own-select 0: only the resolved child 1 (the -1 dest is dropped).
    expect([...surfaceRowSet(0, "own", edges)].sort()).toEqual([0, 1]);
  });

  it("always includes the selected node itself", () => {
    for (const mode of ["own", "surface"] as const) {
      for (let sel = 0; sel <= 4; sel++) {
        expect(surfaceRowSet(sel, mode, EDGES).has(sel)).toBe(true);
      }
    }
  });
});
