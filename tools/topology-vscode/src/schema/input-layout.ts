// input-layout.ts — BINARY encode of the editor→Go input stream.
//
// The TS→Go bridge is a purely BINARY buffer, symmetric with the Go→TS content buffer
// streamed on fd 3. The webview builds a binary RECORD per message here; the extension
// host writes each record FRAMED as [len:u32-LE][record] to Go's stdin. Go decodes it in
// nodes/Wiring/input_codec.go into the SAME stdinMsg the old newline-JSON path produced.
//
// This module is the SINGLE source of the TS-side record layout and is mirrored by the Go
// codec. The two carry an identical fingerprint (INPUT_LAYOUT_FINGERPRINT below ==
// InputLayoutFingerprint in input_codec.go), enforced by tools/check-input-layout-parity.sh.
//
// Numbers are little-endian (matching fd 3). Enum discriminators (event kind, hit kind,
// update entity kind) are u8 indices into the shared orderings. Structural edit payloads
// (node-move entries, edge-faded map, port-anchor keys, overlay state, scene blob) ride as
// a length-prefixed UTF-8 JSON section — the ENVELOPE/transport is binary; only that leaf
// CONTENT stays JSON.

// INPUT_LAYOUT_FINGERPRINT: v1 kinds=resume:1,pause:2,resend:3,raw-input:10,edit-create:20,edit-delete:21,edit-update:22 eventKinds=pointerdown,pointermove,pointerup,wheel,home hitKinds=port,handhold,node,edge,empty updateKinds=node,edge,camera,overlays,scene
export const INPUT_LAYOUT_FINGERPRINT =
  "v1 kinds=resume:1,pause:2,resend:3,raw-input:10,edit-create:20,edit-delete:21,edit-update:22 eventKinds=pointerdown,pointermove,pointerup,wheel,home hitKinds=port,handhold,node,edge,empty updateKinds=node,edge,camera,overlays,scene";

// Record kind bytes (first byte of every record). Must match input_codec.go.
export const IN_KIND_RESUME = 1;
export const IN_KIND_PAUSE = 2;
export const IN_KIND_RESEND = 3;
export const IN_KIND_RAW_INPUT = 10;
export const IN_KIND_EDIT_CREATE = 20;
export const IN_KIND_EDIT_DELETE = 21;
export const IN_KIND_EDIT_UPDATE = 22;

// Enum orderings (u8 index → string), shared with input_codec.go.
export const IN_EVENT_KINDS = ["pointerdown", "pointermove", "pointerup", "wheel", "home"] as const;
export const IN_HIT_KINDS = ["port", "handhold", "node", "edge", "empty"] as const;
export const IN_UPDATE_KINDS = ["node", "edge", "camera", "overlays", "scene"] as const;

import type { RawInputEvent } from "../messages";

function enumIndex(list: readonly string[], s: string): number {
  const i = list.indexOf(s);
  return i < 0 ? 0 : i;
}

// ByteWriter — a little-endian growable record builder.
class ByteWriter {
  private buf = new Uint8Array(64);
  private view = new DataView(this.buf.buffer);
  private pos = 0;

  private ensure(n: number): void {
    if (this.pos + n <= this.buf.length) return;
    let cap = this.buf.length * 2;
    while (cap < this.pos + n) cap *= 2;
    const next = new Uint8Array(cap);
    next.set(this.buf);
    this.buf = next;
    this.view = new DataView(this.buf.buffer);
  }

  u8(v: number): void {
    this.ensure(1);
    this.view.setUint8(this.pos, v);
    this.pos += 1;
  }
  i32(v: number): void {
    this.ensure(4);
    this.view.setInt32(this.pos, v, true);
    this.pos += 4;
  }
  u32(v: number): void {
    this.ensure(4);
    this.view.setUint32(this.pos, v, true);
    this.pos += 4;
  }
  f64(v: number): void {
    this.ensure(8);
    this.view.setFloat64(this.pos, v, true);
    this.pos += 8;
  }
  bool(v: boolean): void {
    this.u8(v ? 1 : 0);
  }
  str(s: string): void {
    const bytes = new TextEncoder().encode(s);
    this.u32(bytes.length);
    this.ensure(bytes.length);
    this.buf.set(bytes, this.pos);
    this.pos += bytes.length;
  }
  /** The record bytes (a fresh ArrayBuffer sized to content). */
  toArrayBuffer(): ArrayBuffer {
    return this.buf.buffer.slice(0, this.pos);
  }
}

/** Build a payload-less control record (play / pause / resend). */
export function encodeControl(kind: number): ArrayBuffer {
  const w = new ByteWriter();
  w.u8(kind);
  return w.toArrayBuffer();
}

export const encodePlay = () => encodeControl(IN_KIND_RESUME);
export const encodePause = () => encodeControl(IN_KIND_PAUSE);
export const encodeResend = () => encodeControl(IN_KIND_RESEND);

/** Build an edit create/delete record: two length-prefixed UTF-8 strings. */
export function encodeEditCreate(target: string, targetHandle: string): ArrayBuffer {
  return encodeCreateDelete(IN_KIND_EDIT_CREATE, target, targetHandle);
}
export function encodeEditDelete(target: string, targetHandle: string): ArrayBuffer {
  return encodeCreateDelete(IN_KIND_EDIT_DELETE, target, targetHandle);
}
function encodeCreateDelete(kind: number, target: string, targetHandle: string): ArrayBuffer {
  const w = new ByteWriter();
  w.u8(kind);
  w.str(target);
  w.str(targetHandle);
  return w.toArrayBuffer();
}

/** Build an edit update record: entity-kind byte + JSON payload leaf. entityKind is one of
 *  IN_UPDATE_KINDS (node/edge/camera/overlays/scene); payload is the typed update message
 *  (its attr/flag/entries/edges/viewpoint/state/scene fields ride the JSON leaf). */
export function encodeEditUpdate(entityKind: (typeof IN_UPDATE_KINDS)[number], payload: unknown): ArrayBuffer {
  const w = new ByteWriter();
  w.u8(IN_KIND_EDIT_UPDATE);
  w.u8(enumIndex(IN_UPDATE_KINDS, entityKind));
  w.str(JSON.stringify(payload));
  return w.toArrayBuffer();
}

/** Build a raw-input record: fully-numeric fixed fields + enum bytes (no JSON). */
export function encodeRawInput(ev: RawInputEvent): ArrayBuffer {
  const w = new ByteWriter();
  w.u8(IN_KIND_RAW_INPUT);
  w.u8(enumIndex(IN_EVENT_KINDS, ev.kind));
  w.f64(ev.x);
  w.f64(ev.y);
  w.f64(ev.rectLeft);
  w.f64(ev.rectTop);
  w.f64(ev.rectWidth);
  w.f64(ev.rectHeight);
  w.i32(ev.button);
  w.bool(ev.ctrl);
  w.bool(ev.shift);
  w.bool(ev.alt);
  w.bool(ev.meta);
  w.f64(ev.deltaX);
  w.f64(ev.deltaY);
  w.f64(ev.fov);
  w.u8(enumIndex(IN_HIT_KINDS, ev.hit.kind));
  w.bool(ev.hit.isInput);
  w.i32(ev.hit.nodeRow);
  w.i32(ev.hit.portRow);
  w.i32(ev.hit.edgeRow);
  w.f64(ev.hit.x);
  w.f64(ev.hit.y);
  w.f64(ev.hit.z);
  return w.toArrayBuffer();
}

/** Wrap a record body with the [len:u32-LE] transport frame (used by the host writer). */
export function frameRecord(record: ArrayBuffer): Uint8Array {
  const rec = new Uint8Array(record);
  const out = new Uint8Array(4 + rec.length);
  new DataView(out.buffer).setUint32(0, rec.length, true);
  out.set(rec, 4);
  return out;
}

// --- Decoder (used by unit tests + round-trip; production decode is input_codec.go) ------

class ByteReader {
  private view: DataView;
  private pos = 1; // skip kind byte
  constructor(private bytes: Uint8Array) {
    this.view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  }
  u8(): number {
    return this.view.getUint8(this.pos++);
  }
  i32(): number {
    const v = this.view.getInt32(this.pos, true);
    this.pos += 4;
    return v;
  }
  u32(): number {
    const v = this.view.getUint32(this.pos, true);
    this.pos += 4;
    return v;
  }
  f64(): number {
    const v = this.view.getFloat64(this.pos, true);
    this.pos += 8;
    return v;
  }
  bool(): boolean {
    return this.u8() !== 0;
  }
  str(): string {
    const n = this.u32();
    const s = new TextDecoder().decode(this.bytes.subarray(this.pos, this.pos + n));
    this.pos += n;
    return s;
  }
}

export type DecodedInput =
  | { kind: "play" | "pause" | "resend" }
  | { kind: "raw-input"; event: RawInputEvent }
  | { kind: "edit-create" | "edit-delete"; target: string; targetHandle: string }
  | { kind: "edit-update"; entity: string; payload: unknown };

/** Decode one record body (with kind byte, without the [len] frame). */
export function decodeInputRecord(record: ArrayBuffer): DecodedInput | undefined {
  const bytes = new Uint8Array(record);
  if (bytes.length === 0) return undefined;
  const r = new ByteReader(bytes);
  switch (bytes[0]) {
    case IN_KIND_RESUME:
      return { kind: "play" };
    case IN_KIND_PAUSE:
      return { kind: "pause" };
    case IN_KIND_RESEND:
      return { kind: "resend" };
    case IN_KIND_RAW_INPUT: {
      const event: RawInputEvent = {
        kind: IN_EVENT_KINDS[r.u8()] ?? "pointermove",
        x: r.f64(),
        y: r.f64(),
        rectLeft: r.f64(),
        rectTop: r.f64(),
        rectWidth: r.f64(),
        rectHeight: r.f64(),
        button: r.i32(),
        ctrl: r.bool(),
        shift: r.bool(),
        alt: r.bool(),
        meta: r.bool(),
        deltaX: r.f64(),
        deltaY: r.f64(),
        fov: r.f64(),
        hit: {
          kind: IN_HIT_KINDS[r.u8()] ?? "empty",
          isInput: r.bool(),
          nodeRow: r.i32(),
          portRow: r.i32(),
          edgeRow: r.i32(),
          x: r.f64(),
          y: r.f64(),
          z: r.f64(),
        },
      };
      return { kind: "raw-input", event };
    }
    case IN_KIND_EDIT_CREATE:
    case IN_KIND_EDIT_DELETE:
      return {
        kind: bytes[0] === IN_KIND_EDIT_CREATE ? "edit-create" : "edit-delete",
        target: r.str(),
        targetHandle: r.str(),
      };
    case IN_KIND_EDIT_UPDATE: {
      const entity = IN_UPDATE_KINDS[r.u8()] ?? "scene";
      return { kind: "edit-update", entity, payload: JSON.parse(r.str()) };
    }
  }
  return undefined;
}
