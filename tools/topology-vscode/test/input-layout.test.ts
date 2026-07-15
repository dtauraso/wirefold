// input-layout binary record tests: exact byte layout for the simple records + encode/
// decode round-trips. The Go decoder (input_codec.go) mirrors this layout; the shared
// fingerprint is parity-guarded by tools/check-input-layout-parity.sh.
import { describe, it, expect } from "vitest";
import {
  IN_KIND_RESUME,
  IN_KIND_PAUSE,
  IN_KIND_RESEND,
  IN_KIND_SAVE,
  IN_KIND_FADE_TOGGLE,
  IN_KIND_EDIT_CREATE,
  IN_KIND_EDIT_DELETE,
  IN_KIND_EDIT_UPDATE,
  IN_UPDATE_KINDS,
  INPUT_LAYOUT_FINGERPRINT,
  encodePlay,
  encodePause,
  encodeResend,
  encodeFadeToggle,
  encodeOverlaysToggle,
  decodeInputRecord,
  frameRecord,
} from "../src/schema/input-layout";
import { OVERLAY_FLAG_ORDER } from "../src/messages";

/** Build a bare kind-byte control record (mirrors ByteWriter's u8-only shape). No live
 *  TS encoder builds a "save"/"edit-create"/"edit-delete" record today (Go still decodes
 *  them and they are in the fingerprint), so tests construct the raw bytes directly. */
function controlBytes(kind: number): ArrayBuffer {
  return new Uint8Array([kind]).buffer;
}

/** Build a [kind][len:u32-LE][utf8][len:u32-LE][utf8] record — the edit create/delete shape. */
function createDeleteBytes(kind: number, target: string, targetHandle: string): ArrayBuffer {
  const enc = new TextEncoder();
  const t = enc.encode(target);
  const h = enc.encode(targetHandle);
  const out = new Uint8Array(1 + 4 + t.length + 4 + h.length);
  const view = new DataView(out.buffer);
  let pos = 0;
  out[pos] = kind; pos += 1;
  view.setUint32(pos, t.length, true); pos += 4;
  out.set(t, pos); pos += t.length;
  view.setUint32(pos, h.length, true); pos += 4;
  out.set(h, pos);
  return out.buffer;
}

describe("control records — exact bytes", () => {
  it("play/pause/resend/save are a single kind byte", () => {
    expect(new Uint8Array(encodePlay())).toEqual(new Uint8Array([IN_KIND_RESUME]));
    expect(new Uint8Array(encodePause())).toEqual(new Uint8Array([IN_KIND_PAUSE]));
    expect(new Uint8Array(encodeResend())).toEqual(new Uint8Array([IN_KIND_RESEND]));
    expect(new Uint8Array(controlBytes(IN_KIND_SAVE))).toEqual(new Uint8Array([IN_KIND_SAVE]));
    expect(new Uint8Array(encodeFadeToggle())).toEqual(new Uint8Array([IN_KIND_FADE_TOGGLE]));
  });

  it("decode control", () => {
    expect(decodeInputRecord(encodePlay())).toEqual({ kind: "play" });
    expect(decodeInputRecord(encodePause())).toEqual({ kind: "pause" });
    expect(decodeInputRecord(encodeResend())).toEqual({ kind: "resend" });
    expect(decodeInputRecord(controlBytes(IN_KIND_SAVE))).toEqual({ kind: "save" });
    expect(decodeInputRecord(encodeFadeToggle())).toEqual({ kind: "fade-toggle" });
  });
});

describe("edit create/delete — exact bytes + round-trip", () => {
  it("create encodes kind byte + two length-prefixed UTF-8 strings", () => {
    const bytes = new Uint8Array(createDeleteBytes(IN_KIND_EDIT_CREATE, "ab", "c"));
    // [20][len=2 LE][a][b][len=1 LE][c]
    expect(bytes).toEqual(new Uint8Array([IN_KIND_EDIT_CREATE, 2, 0, 0, 0, 0x61, 0x62, 1, 0, 0, 0, 0x63]));
  });

  it("round-trips create + delete (incl. multibyte UTF-8)", () => {
    expect(decodeInputRecord(createDeleteBytes(IN_KIND_EDIT_CREATE, "n1", "out"))).toEqual({
      kind: "edit-create",
      target: "n1",
      targetHandle: "out",
    });
    expect(decodeInputRecord(createDeleteBytes(IN_KIND_EDIT_DELETE, "nÖde", "port:β"))).toEqual({
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
    expect(new Uint8Array(encodeOverlaysToggle("overlays"))[3]).toBe(OVERLAY_FLAG_ORDER.indexOf("overlays"));
  });

});

describe("fingerprint self-consistency", () => {
  it("overlayFlags token equals OVERLAY_FLAG_ORDER", () => {
    expect(INPUT_LAYOUT_FINGERPRINT).toContain(`overlayFlags=${OVERLAY_FLAG_ORDER.join(",")}`);
  });
});

describe("frameRecord", () => {
  it("prefixes the record with its u32-LE length", () => {
    const rec = createDeleteBytes(IN_KIND_EDIT_CREATE, "a", "b");
    const framed = frameRecord(rec);
    const len = new DataView(framed.buffer, framed.byteOffset, 4).getUint32(0, true);
    expect(len).toBe(rec.byteLength);
    expect(framed.subarray(4)).toEqual(new Uint8Array(rec));
  });
});
