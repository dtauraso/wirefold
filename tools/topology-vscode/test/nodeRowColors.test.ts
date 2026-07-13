// Unit tests for the KindId→NODE_DEFS_ARRAY color-mapping.
// Tests that:
//   - NODE_DEFS_ARRAY is in the expected alphabetical Go-kind order (matching kindIDMap)
//   - readNodeKindId decodes the buffer KindId byte correctly
//   - The fill/stroke colors at each KindId index match the expected NODE_DEFS entry

import { describe, it, expect } from "vitest";
import { NODE_DEFS, NODE_DEFS_ARRAY } from "../src/schema/node-defs";
import { NODE_COL_KIND_ID, NODE_STRIDE, readNodeKindId } from "../src/schema/buffer-layout";

function makeNodeView(kindId: number): DataView {
  const buf = new ArrayBuffer(NODE_STRIDE);
  const dv = new DataView(buf);
  dv.setUint8(NODE_COL_KIND_ID, kindId);
  return dv;
}

// NODE_DEFS_ARRAY must match the alphabetical Go-kind order produced by kindIDMap
// in Buffer/node_kind_id_gen.go. Index i here <-> KindId i in the buffer node block.
const EXPECTED_ORDER = [
  "Hold",
  "HoldFlip",
  "HoldNewSendOld",
  "Input",
  "Pacer",
  "Pulse",
  "StartHoldNewSendOld",
  "WindowAndInhibitLeftGate",
  "WindowAndInhibitRightGate",
] as const;

describe("NODE_DEFS_ARRAY order parity with Go kindIDMap", () => {
  it("has 9 entries", () => {
    expect(NODE_DEFS_ARRAY.length).toBe(9);
  });

  it("entries are in alphabetical Go-kind order matching kindIDMap", () => {
    for (let i = 0; i < EXPECTED_ORDER.length; i++) {
      const kind = EXPECTED_ORDER[i];
      expect(NODE_DEFS_ARRAY[i]).toEqual(NODE_DEFS[kind]);
    }
  });
});

describe("readNodeKindId decodes buffer KindId byte", () => {
  it("reads kindId 0 (Hold) from buffer", () => {
    expect(readNodeKindId(makeNodeView(0), 0)).toBe(0);
  });

  it("reads kindId 5 (Pulse) from buffer", () => {
    expect(readNodeKindId(makeNodeView(5), 0)).toBe(5);
  });

  it("reads kindId 0xFF (unknown sentinel) from buffer", () => {
    expect(readNodeKindId(makeNodeView(0xFF), 0)).toBe(0xFF);
  });
});

describe("KindId to fill/stroke color mapping", () => {
  // Simulates nodeRowColors: read KindId from buffer, index NODE_DEFS_ARRAY.
  function colorsForKindId(kindId: number) {
    const def = NODE_DEFS_ARRAY[kindId];
    return { fill: def?.fill ?? "#ffffff", stroke: def?.stroke ?? "#888888" };
  }

  it("kindId=0 (Hold) produces Hold fill/stroke", () => {
    const { fill, stroke } = colorsForKindId(readNodeKindId(makeNodeView(0), 0));
    expect(fill).toBe(NODE_DEFS.Hold.fill);
    expect(stroke).toBe(NODE_DEFS.Hold.stroke);
  });

  it("kindId=5 (Pulse) produces Pulse fill/stroke", () => {
    const { fill, stroke } = colorsForKindId(readNodeKindId(makeNodeView(5), 0));
    expect(fill).toBe(NODE_DEFS.Pulse.fill);
    expect(stroke).toBe(NODE_DEFS.Pulse.stroke);
  });

  it("kindId=0xFF (unknown) falls back to grey defaults", () => {
    const { fill, stroke } = colorsForKindId(0xFF);
    expect(fill).toBe("#ffffff");
    expect(stroke).toBe("#888888");
  });
});
