// beadStyle.test.ts — the buffer transit-bead renderer (BeadInstances) and the JSON
// PulseBead both color on-wire beads via beadStyleForValue. Pin the value→fill/ring
// mapping and the "invalid value → hidden (no style)" rule so a buffer transit bead
// cannot diverge from the pre-branch PulseBead look.
import { describe, it, expect } from "vitest";
import { beadStyleForValue } from "../src/webview/three/bead-style";

describe("beadStyleForValue (transit + interior bead color source of truth)", () => {
  it("value 0 → black fill, black ring", () => {
    expect(beadStyleForValue(0)).toEqual({ fill: "#000000", ring: "#000000" });
  });

  it("value 1 → white fill, black ring", () => {
    expect(beadStyleForValue(1)).toEqual({ fill: "#ffffff", ring: "#000000" });
  });

  it("invalid values have no style → caller hides the bead", () => {
    for (const v of [-1, 2, 42, NaN, null, undefined]) {
      expect(beadStyleForValue(v as number)).toBeUndefined();
    }
  });
});
