// parseRawInput validation tests. parseWebviewToHost DROPS any raw-input event whose
// hit.kind is not in RAW_HIT_KINDS. An earlier omission of "edge" silently killed EVERY
// gesture (plain-wheel pan included) while the cursor was over an edge — and over a node,
// whose incident edge pick-halos classify as "edge" first. These pin every hit-kind through
// the validator so a dropped variant can't recur.
import { describe, it, expect } from "vitest";
import { parseWebviewToHost } from "../src/messages";

function rawInput(hit: Record<string, unknown>) {
  return {
    type: "raw-input",
    event: {
      kind: "wheel",
      x: 400, y: 300,
      rectLeft: 0, rectTop: 0, rectWidth: 800, rectHeight: 600,
      button: -1,
      ctrl: false, shift: false, alt: false, meta: false,
      deltaX: 40, deltaY: 0,
      fov: 50,
      hit,
    },
  };
}

const baseHit = { isInput: false, nodeRow: -1, portRow: -1, edgeRow: -1, x: 0, y: 0, z: 0 };

describe("parseRawInput hit kinds", () => {
  for (const kind of ["port", "handhold", "node", "edge", "empty"]) {
    it(`accepts a wheel event with a ${kind} hit`, () => {
      const msg = rawInput({ ...baseHit, kind });
      expect(parseWebviewToHost(msg)).toEqual(msg);
    });
  }

  it("accepts a plain-wheel pan over an EDGE (regression: edge hits were dropped)", () => {
    const msg = rawInput({ ...baseHit, kind: "edge", edgeRow: 2 });
    expect(parseWebviewToHost(msg)).toEqual(msg);
  });

  it("drops an unknown hit kind", () => {
    expect(parseWebviewToHost(rawInput({ ...baseHit, kind: "bogus" }))).toBeUndefined();
  });

  it("drops a hit with a non-numeric edgeRow", () => {
    expect(
      parseWebviewToHost(rawInput({ ...baseHit, kind: "edge", edgeRow: "2" })),
    ).toBeUndefined();
  });
});
