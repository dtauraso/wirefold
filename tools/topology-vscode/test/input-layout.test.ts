// input-layout binary record tests: exact byte layout for the simple records + encode/
// decode round-trips. The Go decoder (input_codec.go) mirrors this layout; the shared
// fingerprint is parity-guarded by tools/check-input-layout-parity.sh.
import { describe, it, expect } from "vitest";
import {
  IN_KIND_SAVE,
  IN_KIND_RAW_INPUT,
  IN_KIND_EDIT_UPDATE,
  IN_EVENT_KINDS,
  IN_HIT_KINDS,
  IN_UPDATE_KINDS,
  IN_UPDATE_ATTRS,
  INPUT_LAYOUT_FINGERPRINT,
  encodeOverlaysToggle,
  encodeClockSpeed,
  decodeInputRecord,
  frameRecord,
} from "../src/schema/input-layout";
import { OVERLAY_FLAG_ORDER } from "../src/messages";

/** Build a bare kind-byte control record (mirrors ByteWriter's u8-only shape). No live
 *  TS encoder builds a "save" record today (Go still decodes it and it is in the
 *  fingerprint), so tests construct the raw bytes directly. */
function controlBytes(kind: number): ArrayBuffer {
  return new Uint8Array([kind]).buffer;
}

describe("control records — exact bytes", () => {
  it("save is a single kind byte", () => {
    expect(new Uint8Array(controlBytes(IN_KIND_SAVE))).toEqual(new Uint8Array([IN_KIND_SAVE]));
  });

  it("save is pinned to the literal byte 0x04 (Go's IN_KIND_SAVE)", () => {
    // Pins the actual wire byte Go's stdin_reader.go decoder expects for "save". If
    // IN_KIND_SAVE ever changes value, this literal must be independently updated to match
    // Go, or this assertion catches the drift.
    expect(new Uint8Array(controlBytes(IN_KIND_SAVE))).toEqual(new Uint8Array([0x04]));
  });

  it("decode control", () => {
    expect(decodeInputRecord(controlBytes(IN_KIND_SAVE))).toEqual({ kind: "save" });
  });
});

describe("overlays edit-update — fully numeric (no JSON)", () => {
  it("toggle: [22][entityKind=overlays][attr=toggle=0][flagId] + round-trip", () => {
    const rec = encodeOverlaysToggle("tori");
    const b = new Uint8Array(rec);
    // Literal bytes pinned against the current wire contract, not re-derived from the
    // same enumIndex() call the encoder uses — a drift in IN_KIND_EDIT_UPDATE, in
    // IN_UPDATE_KINDS ordering, or in OVERLAY_FLAG_ORDER ordering must change one of
    // these literals too, or the assertion fails.
    expect(b[0]).toBe(22); // IN_KIND_EDIT_UPDATE
    expect(b[1]).toBe(0); // "overlays" is IN_UPDATE_KINDS[0]
    expect(b[2]).toBe(0); // attr=toggle
    expect(b[3]).toBe(0); // "tori" is OVERLAY_FLAG_ORDER[0]
    expect(decodeInputRecord(rec)).toEqual({ kind: "edit-update", entity: "overlays", attr: "toggle", flag: "tori" });
    // A non-zero flag maps by index — "overlays" is the last entry in OVERLAY_FLAG_ORDER (index 6).
    expect(new Uint8Array(encodeOverlaysToggle("overlays"))[3]).toBe(6);
  });

});

describe("clock edit-update — fully numeric (no JSON)", () => {
  it("speed: [22][entityKind=clock][attr=speed=1][value] + round-trip", () => {
    const rec = encodeClockSpeed(2);
    const b = new Uint8Array(rec);
    expect(b).toEqual(new Uint8Array([22, IN_UPDATE_KINDS.indexOf("clock"), 1, 2]));
    expect(decodeInputRecord(rec)).toEqual({ kind: "edit-update", entity: "clock", attr: "speed", value: 2 });
  });
});

describe("fingerprint self-consistency", () => {
  it("overlayFlags token equals OVERLAY_FLAG_ORDER", () => {
    expect(INPUT_LAYOUT_FINGERPRINT).toContain(`overlayFlags=${OVERLAY_FLAG_ORDER.join(",")}`);
  });

  // These orderings are WIRE INDICES: only a u8 index crosses the bridge, so a reorder on
  // either side silently re-points every value (a raycast hit on a node decoding as an
  // edge) with nothing to fail — no type error, no crash, a valid byte either way.
  // check-input-layout-parity.sh only diffs the two FINGERPRINT STRINGS, so it catches
  // half-remembering (array + one fingerprint edited) and is blind to forgetting (array
  // edited, neither fingerprint touched). These tests pin the TS end of each chain; Go
  // derives its arrays from its own fingerprint (input_codec.go parseFPList), so the loop
  // TS array → token → [guard] → Go token → Go array is closed at every link.
  it.each([
    ["eventKinds", IN_EVENT_KINDS],
    ["hitKinds", IN_HIT_KINDS],
    ["updateKinds", IN_UPDATE_KINDS],
    ["updateAttrs", IN_UPDATE_ATTRS],
  ])("%s token equals the live array (not a string literal copy)", (marker, arr) => {
    expect(INPUT_LAYOUT_FINGERPRINT).toContain(`${marker}=${arr.join(",")}`);
  });

  it("kinds= token matches the actual IN_KIND_* constants (not a string literal copy)", () => {
    // Built from the live constants, not typed in by hand — if any IN_KIND_* value drifts
    // from the fingerprint's hardcoded numbers, this fails instead of silently passing.
    const expected =
      `kinds=save:${IN_KIND_SAVE},raw-input:${IN_KIND_RAW_INPUT},` +
      `edit-update:${IN_KIND_EDIT_UPDATE}`;
    expect(INPUT_LAYOUT_FINGERPRINT).toContain(expected);
  });
});

describe("frameRecord", () => {
  it("prefixes the record with its u32-LE length", () => {
    const rec = controlBytes(IN_KIND_SAVE);
    const framed = frameRecord(rec);
    const len = new DataView(framed.buffer, framed.byteOffset, 4).getUint32(0, true);
    expect(len).toBe(rec.byteLength);
    expect(framed.subarray(4)).toEqual(new Uint8Array(rec));
  });
});
