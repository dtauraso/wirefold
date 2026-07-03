// input-layout binary record tests: exact byte layout for the simple records + encode/
// decode round-trips. The Go decoder (input_codec.go) mirrors this layout; the shared
// fingerprint is parity-guarded by tools/check-input-layout-parity.sh.
import { describe, it, expect } from "vitest";
import {
  IN_KIND_RESUME,
  IN_KIND_PAUSE,
  IN_KIND_RESEND,
  IN_KIND_SAVE,
  IN_KIND_EDIT_CREATE,
  IN_KIND_EDIT_UPDATE,
  IN_UPDATE_KINDS,
  INPUT_LAYOUT_FINGERPRINT,
  encodePlay,
  encodePause,
  encodeResend,
  encodeSave,
  encodeEditCreate,
  encodeEditDelete,
  encodeOverlaysToggle,
  encodeOverlaysSet,
  decodeInputRecord,
  frameRecord,
} from "../src/schema/input-layout";
import { OVERLAY_FLAG_ORDER, type OverlayState } from "../src/messages";

describe("control records — exact bytes", () => {
  it("play/pause/resend/save are a single kind byte", () => {
    expect(new Uint8Array(encodePlay())).toEqual(new Uint8Array([IN_KIND_RESUME]));
    expect(new Uint8Array(encodePause())).toEqual(new Uint8Array([IN_KIND_PAUSE]));
    expect(new Uint8Array(encodeResend())).toEqual(new Uint8Array([IN_KIND_RESEND]));
    expect(new Uint8Array(encodeSave())).toEqual(new Uint8Array([IN_KIND_SAVE]));
  });

  it("decode control", () => {
    expect(decodeInputRecord(encodePlay())).toEqual({ kind: "play" });
    expect(decodeInputRecord(encodePause())).toEqual({ kind: "pause" });
    expect(decodeInputRecord(encodeResend())).toEqual({ kind: "resend" });
    expect(decodeInputRecord(encodeSave())).toEqual({ kind: "save" });
  });
});

describe("edit create/delete — exact bytes + round-trip", () => {
  it("create encodes kind byte + two length-prefixed UTF-8 strings", () => {
    const bytes = new Uint8Array(encodeEditCreate("ab", "c"));
    // [20][len=2 LE][a][b][len=1 LE][c]
    expect(bytes).toEqual(new Uint8Array([IN_KIND_EDIT_CREATE, 2, 0, 0, 0, 0x61, 0x62, 1, 0, 0, 0, 0x63]));
  });

  it("round-trips create + delete (incl. multibyte UTF-8)", () => {
    expect(decodeInputRecord(encodeEditCreate("n1", "out"))).toEqual({
      kind: "edit-create",
      target: "n1",
      targetHandle: "out",
    });
    expect(decodeInputRecord(encodeEditDelete("nÖde", "port:β"))).toEqual({
      kind: "edit-delete",
      target: "nÖde",
      targetHandle: "port:β",
    });
  });
});

describe("overlays edit-update — fully numeric (no JSON)", () => {
  it("toggle: [22][entityKind=overlays][attr=toggle=0][flagId] + round-trip", () => {
    const rec = encodeOverlaysToggle("tori");
    const b = new Uint8Array(rec);
    expect(b[0]).toBe(IN_KIND_EDIT_UPDATE);
    expect(b[1]).toBe(IN_UPDATE_KINDS.indexOf("overlays"));
    expect(b[2]).toBe(0); // attr=toggle
    expect(b[3]).toBe(OVERLAY_FLAG_ORDER.indexOf("tori"));
    expect(decodeInputRecord(rec)).toEqual({ kind: "edit-update", entity: "overlays", attr: "toggle", flag: "tori" });
    // A non-zero flag maps by index.
    expect(new Uint8Array(encodeOverlaysToggle("doubleLinks"))[3]).toBe(OVERLAY_FLAG_ORDER.indexOf("doubleLinks"));
  });

  it("set: [22][entityKind=overlays][attr=set=1][u16 bitfield] + round-trip", () => {
    const state = Object.fromEntries(OVERLAY_FLAG_ORDER.map((f) => [f, false])) as OverlayState;
    state.tori = true;
    state.overlays = true;
    const rec = encodeOverlaysSet(state);
    const b = new Uint8Array(rec);
    expect(b[0]).toBe(IN_KIND_EDIT_UPDATE);
    expect(b[2]).toBe(1); // attr=set
    // bits: tori(0) + overlays(8) → 0x0101 LE
    expect(b[3]).toBe(0x01);
    expect(b[4]).toBe(0x01);
    const decoded = decodeInputRecord(rec);
    expect(decoded).toEqual({ kind: "edit-update", entity: "overlays", attr: "set", state });
  });
});

describe("fingerprint self-consistency", () => {
  it("overlayFlags token equals OVERLAY_FLAG_ORDER", () => {
    expect(INPUT_LAYOUT_FINGERPRINT).toContain(`overlayFlags=${OVERLAY_FLAG_ORDER.join(",")}`);
  });
});

describe("frameRecord", () => {
  it("prefixes the record with its u32-LE length", () => {
    const rec = encodeEditCreate("a", "b");
    const framed = frameRecord(rec);
    const len = new DataView(framed.buffer, framed.byteOffset, 4).getUint32(0, true);
    expect(len).toBe(rec.byteLength);
    expect(framed.subarray(4)).toEqual(new Uint8Array(rec));
  });
});
