// Raw-input BINARY round-trip tests. The TS→Go bridge is now a binary buffer: the webview
// encodes a raw-input event into a binary record (encodeRawInput) and Go decodes it. These
// pin every hit-kind through the encode/decode round-trip so a dropped/reordered variant
// can't recur — an earlier omission of "edge" silently killed EVERY gesture (plain-wheel
// pan included) while the cursor was over an edge (and over a node, whose incident edge
// pick-halos classify as "edge" first).
import { describe, it, expect } from "vitest";
import { encodeRawInput, decodeInputRecord } from "../src/schema/input-layout";
import type { RawInputEvent, RawHit } from "../src/messages";

function rawInput(hit: RawHit): RawInputEvent {
  return {
    kind: "wheel",
    x: 400, y: 300,
    rectLeft: 0, rectTop: 0, rectWidth: 800, rectHeight: 600,
    button: -1,
    ctrl: false, shift: false, alt: false, meta: false,
    deltaX: 40, deltaY: 0,
    fov: 50,
    hit,
  };
}

const baseHit = { isInput: false, nodeRow: -1, portRow: -1, edgeRow: -1, handholdTerm: -1 };

describe("raw-input binary round-trip — hit kinds", () => {
  for (const kind of ["port", "handhold", "node", "edge", "empty"] as const) {
    it(`round-trips a wheel event with a ${kind} hit`, () => {
      const ev = rawInput({ ...baseHit, kind });
      const decoded = decodeInputRecord(encodeRawInput(ev));
      expect(decoded).toEqual({ kind: "raw-input", event: ev });
    });
  }

  it("round-trips a plain-wheel pan over an EDGE (regression: edge hits were dropped)", () => {
    const ev = rawInput({ ...baseHit, kind: "edge", edgeRow: 2 });
    const decoded = decodeInputRecord(encodeRawInput(ev));
    expect(decoded).toEqual({ kind: "raw-input", event: ev });
  });

  it("preserves numeric hit rows + flags exactly", () => {
    const ev = rawInput({ kind: "node", isInput: true, nodeRow: 7, portRow: -1, edgeRow: -1, handholdTerm: -1 });
    const decoded = decodeInputRecord(encodeRawInput(ev));
    expect(decoded).toEqual({ kind: "raw-input", event: ev });
  });
});
