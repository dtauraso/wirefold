// input-layout binary record tests: exact byte layout for the simple records + encode/
// decode round-trips. The Go decoder (input_codec.go) mirrors this layout; the shared
// fingerprint is parity-guarded by tools/check-input-layout-parity.sh.
import { describe, it, expect } from "vitest";
import {
  IN_KIND_RESUME,
  IN_KIND_PAUSE,
  IN_KIND_RESEND,
  IN_KIND_EDIT_CREATE,
  IN_KIND_EDIT_UPDATE,
  IN_UPDATE_KINDS,
  encodePlay,
  encodePause,
  encodeResend,
  encodeEditCreate,
  encodeEditDelete,
  encodeEditUpdate,
  decodeInputRecord,
  frameRecord,
} from "../src/schema/input-layout";

describe("control records — exact bytes", () => {
  it("play/pause/resend are a single kind byte", () => {
    expect(new Uint8Array(encodePlay())).toEqual(new Uint8Array([IN_KIND_RESUME]));
    expect(new Uint8Array(encodePause())).toEqual(new Uint8Array([IN_KIND_PAUSE]));
    expect(new Uint8Array(encodeResend())).toEqual(new Uint8Array([IN_KIND_RESEND]));
  });

  it("decode control", () => {
    expect(decodeInputRecord(encodePlay())).toEqual({ kind: "play" });
    expect(decodeInputRecord(encodePause())).toEqual({ kind: "pause" });
    expect(decodeInputRecord(encodeResend())).toEqual({ kind: "resend" });
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

describe("edit update — entity byte + JSON leaf", () => {
  it("second byte is the entity-kind index; payload survives round-trip", () => {
    const payload = { type: "edit", op: "update", kind: "overlays", attr: "toggle", flag: "tori" };
    const rec = encodeEditUpdate("overlays", payload);
    expect(new Uint8Array(rec)[0]).toBe(IN_KIND_EDIT_UPDATE);
    expect(new Uint8Array(rec)[1]).toBe(IN_UPDATE_KINDS.indexOf("overlays"));
    expect(decodeInputRecord(rec)).toEqual({ kind: "edit-update", entity: "overlays", payload });
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
