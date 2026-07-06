// input-layout.ts — BINARY encode of the editor→Go input stream.
//
// The TS→Go bridge is a purely BINARY buffer, symmetric with the Go→TS content buffer
// streamed on fd 3. The webview builds a binary RECORD per message here; the extension
// host writes each record FRAMED as [len:u32-LE][record] to Go's stdin. Go decodes it in
// nodes/Wiring/input_codec.go into the SAME stdinMsg the dispatch loop consumes.
//
// This module is the SINGLE source of the TS-side record layout and is mirrored by the Go
// codec. The two carry an identical fingerprint (INPUT_LAYOUT_FINGERPRINT below ==
// InputLayoutFingerprint in input_codec.go), enforced by tools/check-input-layout-parity.sh.
//
// Numbers are little-endian (matching fd 3). Enum discriminators (event kind, hit kind,
// update entity kind, update attr, overlay flag) are u8 indices into the shared orderings.
// There is NO JSON on the wire: every record is fully numeric. The live editor→Go traffic
// is raw-input (numeric), overlays toggle (numeric flag-id), the bare save
// COMMAND (kind byte only — Go persists its OWN authoritative scene state), and the
// play/pause/resend control bytes. The create/delete/edit-update record kinds stay defined
// (the 3-op create/update/delete concept) though the gesture FSM now produces edge
// create/delete in-process from raw-input, so TS sends no create/delete today.

// INPUT_LAYOUT_FINGERPRINT: v9 kinds=resume:1,pause:2,resend:3,save:4,fadeToggle:5,clearRule:6,deleteSelectedLock:7,raw-input:10,edit-create:20,edit-delete:21,edit-update:22 eventKinds=pointerdown,pointermove,pointerup,wheel,home hitKinds=port,handhold,node,edge,torus,empty updateKinds=overlays,lock updateAttrs=toggle,active,selected,author,preview authorActions=begin,latch,node,port,torus overlayFlags=tori,scenePoles,nodePoles,angleLabels,selSpherePoles,handholds,labelsGlobal,badgesGlobal,overlays
export const INPUT_LAYOUT_FINGERPRINT =
  "v9 kinds=resume:1,pause:2,resend:3,save:4,fadeToggle:5,clearRule:6,deleteSelectedLock:7,raw-input:10,edit-create:20,edit-delete:21,edit-update:22 eventKinds=pointerdown,pointermove,pointerup,wheel,home hitKinds=port,handhold,node,edge,torus,empty updateKinds=overlays,lock updateAttrs=toggle,active,selected,author,preview authorActions=begin,latch,node,port,torus overlayFlags=tori,scenePoles,nodePoles,angleLabels,selSpherePoles,handholds,labelsGlobal,badgesGlobal,overlays";

// Record kind bytes (first byte of every record). Must match input_codec.go.
export const IN_KIND_RESUME = 1;
export const IN_KIND_PAUSE = 2;
export const IN_KIND_RESEND = 3;
export const IN_KIND_SAVE = 4;
export const IN_KIND_FADE_TOGGLE = 5;
export const IN_KIND_CLEAR_RULE = 6;
export const IN_KIND_DELETE_SELECTED_LOCK = 7;
export const IN_KIND_RAW_INPUT = 10;
export const IN_KIND_EDIT_CREATE = 20;
export const IN_KIND_EDIT_DELETE = 21;
export const IN_KIND_EDIT_UPDATE = 22;

// Enum orderings (u8 index → string), shared with input_codec.go.
export const IN_EVENT_KINDS = ["pointerdown", "pointermove", "pointerup", "wheel", "home"] as const;
export const IN_HIT_KINDS = ["port", "handhold", "node", "edge", "torus", "empty"] as const;
// IN_UPDATE_KINDS is the shared edit-update ENTITY vocabulary. It is the 3rd parity source
// (with messages.ts EditMsg kinds + stdin_reader.go applyUpdate) checked by
// check-edit-op-parity.sh axis 2 — the sentinels below bound that extraction. overlays is
// the sole live edit-update entity; camera/node/edge left the wire when their edits became
// gesture-FSM-in-process (raw-input), and scene became the bare save COMMAND.
// EDIT_UPDATE_KINDS_START
export const IN_UPDATE_KINDS = ["overlays", "lock"] as const;
// EDIT_UPDATE_KINDS_END
// IN_UPDATE_ATTRS is the shared update sub-attribute vocabulary, u8 index on the wire.
// "toggle" is overlays' single-flag toggle. "active"/"selected" are lock's two attrs (both
// carry an i32 md.polarEqs index payload): active flips ToggleLockActive, selected calls
// SelectLock. "author" drives the keyboard-authoring builder (gesture.go Author* methods);
// "preview" sets a keyboard-authoring preview highlight (mirrors pointer hover). The former
// "set" full-visibility-snapshot attr was removed (its only caller, the load-time main.tsx
// push, was deleted; no live TS sender remained).
export const IN_UPDATE_ATTRS = ["toggle", "active", "selected", "author", "preview"] as const;
// IN_AUTHOR_ACTIONS is the attr="author" payload's action sub-discriminator (u8 index on the
// wire, first byte of the author payload after the attr byte). Must match inAuthorActions
// ordering in input_codec.go.
export const IN_AUTHOR_ACTIONS = ["begin", "latch", "node", "port", "torus"] as const;

import type { RawInputEvent, OverlayFlag } from "../messages";
import { OVERLAY_FLAG_ORDER } from "../messages";

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
  u16(v: number): void {
    this.ensure(2);
    this.view.setUint16(this.pos, v, true);
    this.pos += 2;
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

/** Build a payload-less control/command record (play / pause / resend / save). */
export function encodeControl(kind: number): ArrayBuffer {
  const w = new ByteWriter();
  w.u8(kind);
  return w.toArrayBuffer();
}

export const encodePlay = () => encodeControl(IN_KIND_RESUME);
export const encodePause = () => encodeControl(IN_KIND_PAUSE);
export const encodeResend = () => encodeControl(IN_KIND_RESEND);
/** Bare SAVE command: Go persists its OWN authoritative scene state (camera + overlay
 *  visibility). No payload — the editor holds no authoritative scene document to send. */
export const encodeSave = () => encodeControl(IN_KIND_SAVE);
/** Bare FADE-TOGGLE command: toggle fade on Go's CURRENT selection (the "f" key press).
 *  Go owns selection + topology, so no id crosses the wire — just the kind byte. */
export const encodeFadeToggle = () => encodeControl(IN_KIND_FADE_TOGGLE);

/** Clear the in-progress polar equation the rule-builder is authoring (pending term +
 *  accumulated terms). Bare command — Go owns the state (gesture.go clearRuleBuilding). */
export const encodeClearRule = () => encodeControl(IN_KIND_CLEAR_RULE);

/** Delete the panel-focused committed polar-equation lock (selectedLockIndex). Bare command
 *  — Go owns the state and re-guards (locks.go DeleteSelectedLock only deletes when that
 *  equation is deactivated); TS only sends this when the focused row is already inactive. */
export const encodeDeleteSelectedLock = () => encodeControl(IN_KIND_DELETE_SELECTED_LOCK);

// Update attr indices (must match IN_UPDATE_ATTRS ordering).
const IN_OVERLAY_ATTR_TOGGLE = 0;
const IN_LOCK_ATTR_ACTIVE = 1;
const IN_LOCK_ATTR_SELECTED = 2;
const IN_LOCK_ATTR_AUTHOR = 3;
const IN_LOCK_ATTR_PREVIEW = 4;

/** Build an edit create/delete record: two length-prefixed UTF-8 strings. Kept for the
 *  3-op (create/update/delete) codec concept; no live TS caller (the FSM creates/deletes
 *  edges in-process from raw-input). */
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

/** Build an overlays TOGGLE record: [22][entityKind=overlays][attr=toggle][u8 flagId].
 *  flagId is the index of `flag` in OVERLAY_FLAG_ORDER — no flag name crosses the wire. */
export function encodeOverlaysToggle(flag: OverlayFlag): ArrayBuffer {
  const w = new ByteWriter();
  w.u8(IN_KIND_EDIT_UPDATE);
  w.u8(enumIndex(IN_UPDATE_KINDS, "overlays"));
  w.u8(IN_OVERLAY_ATTR_TOGGLE);
  w.u8(enumIndex(OVERLAY_FLAG_ORDER, flag));
  return w.toArrayBuffer();
}

/** Build a lock ACTIVE-toggle record: [22][entityKind=lock][attr=active][i32 index].
 *  index is the md.polarEqs index the PolarLock block row carries (locks.go ToggleLockActive). */
export function encodeLockToggleActive(index: number): ArrayBuffer {
  const w = new ByteWriter();
  w.u8(IN_KIND_EDIT_UPDATE);
  w.u8(enumIndex(IN_UPDATE_KINDS, "lock"));
  w.u8(IN_LOCK_ATTR_ACTIVE);
  w.i32(index);
  return w.toArrayBuffer();
}

/** Build a lock SELECT record: [22][entityKind=lock][attr=selected][i32 index]. Focuses
 *  md.polarEqs[index] as the panel's clicked row (locks.go SelectLock). */
export function encodeLockSelect(index: number): ArrayBuffer {
  const w = new ByteWriter();
  w.u8(IN_KIND_EDIT_UPDATE);
  w.u8(enumIndex(IN_UPDATE_KINDS, "lock"));
  w.u8(IN_LOCK_ATTR_SELECTED);
  w.i32(index);
  return w.toArrayBuffer();
}

/** Build an author BEGIN record: [22][lock][author][begin][u8 eqKind]. Starts (or restarts) a
 *  keyboard-authored equation of the given kind (gesture.go AuthorBegin). */
export function encodeAuthorBegin(eqKind: number): ArrayBuffer {
  const w = new ByteWriter();
  w.u8(IN_KIND_EDIT_UPDATE);
  w.u8(enumIndex(IN_UPDATE_KINDS, "lock"));
  w.u8(IN_LOCK_ATTR_AUTHOR);
  w.u8(enumIndex(IN_AUTHOR_ACTIONS, "begin"));
  w.u8(eqKind);
  return w.toArrayBuffer();
}

/** Build an author NODE record: [22][lock][author][node][i32 nodeRow]. Applies the resolved
 *  node to the builder EXACTLY like a node click: with a half-term pending it completes the
 *  term (committing the equation on the 2nd completed term); otherwise it latches the node as
 *  the rule-builder's Center. Go decides center-vs-term (gesture.go AuthorNode) — the caller
 *  does not need to know which. */
export function encodeAuthorNode(nodeRow: number): ArrayBuffer {
  const w = new ByteWriter();
  w.u8(IN_KIND_EDIT_UPDATE);
  w.u8(enumIndex(IN_UPDATE_KINDS, "lock"));
  w.u8(IN_LOCK_ATTR_AUTHOR);
  w.u8(enumIndex(IN_AUTHOR_ACTIONS, "node"));
  w.i32(nodeRow);
  return w.toArrayBuffer();
}

/** Build an author LATCH record: [22][lock][author][latch][u8 comp][u8 signBit]. Latches a
 *  pending half-term (comp, sign) WITHOUT touching a node — the typed equivalent of a handhold
 *  click (gesture.go AuthorLatchHalfTerm). signBit: 0 = +1, 1 = -1. */
export function encodeAuthorLatch(comp: number, sign: number): ArrayBuffer {
  const w = new ByteWriter();
  w.u8(IN_KIND_EDIT_UPDATE);
  w.u8(enumIndex(IN_UPDATE_KINDS, "lock"));
  w.u8(IN_LOCK_ATTR_AUTHOR);
  w.u8(enumIndex(IN_AUTHOR_ACTIONS, "latch"));
  w.u8(comp);
  w.u8(sign < 0 ? 1 : 0);
  return w.toArrayBuffer();
}

/** Build an author PORT record: [22][lock][author][port][i32 nodeRow][str portName][bool
 *  isInput]. Latches (or completes, if a torus is pending) the `port ∈ torus` pair's port
 *  side (gesture.go AuthorPort). */
export function encodeAuthorPort(nodeRow: number, portName: string, isInput: boolean): ArrayBuffer {
  const w = new ByteWriter();
  w.u8(IN_KIND_EDIT_UPDATE);
  w.u8(enumIndex(IN_UPDATE_KINDS, "lock"));
  w.u8(IN_LOCK_ATTR_AUTHOR);
  w.u8(enumIndex(IN_AUTHOR_ACTIONS, "port"));
  w.i32(nodeRow);
  w.str(portName);
  w.bool(isInput);
  return w.toArrayBuffer();
}

/** Build an author TORUS record: [22][lock][author][torus][i32 nodeRow]. Latches (or
 *  completes) the `port ∈ torus` pair's torus side (gesture.go AuthorTorus). */
export function encodeAuthorTorus(nodeRow: number): ArrayBuffer {
  const w = new ByteWriter();
  w.u8(IN_KIND_EDIT_UPDATE);
  w.u8(enumIndex(IN_UPDATE_KINDS, "lock"));
  w.u8(IN_LOCK_ATTR_AUTHOR);
  w.u8(enumIndex(IN_AUTHOR_ACTIONS, "torus"));
  w.i32(nodeRow);
  return w.toArrayBuffer();
}

/** Build a preview NODE record: [22][lock][preview][previewKind=1(node)][i32 nodeRow]. Sets
 *  the keyboard-authoring preview highlight to a node/torus target (gesture.go
 *  SetHoverNodeByRow). */
export function encodePreviewNode(nodeRow: number): ArrayBuffer {
  const w = new ByteWriter();
  w.u8(IN_KIND_EDIT_UPDATE);
  w.u8(enumIndex(IN_UPDATE_KINDS, "lock"));
  w.u8(IN_LOCK_ATTR_PREVIEW);
  w.u8(1); // previewKind = node
  w.i32(nodeRow);
  return w.toArrayBuffer();
}

/** Build a preview PORT record: [22][lock][preview][previewKind=0(port)][i32 nodeRow][str
 *  portName][bool isInput]. Sets the keyboard-authoring preview highlight to a port target
 *  (gesture.go SetHoverPortByRow). */
export function encodePreviewPort(nodeRow: number, portName: string, isInput: boolean): ArrayBuffer {
  const w = new ByteWriter();
  w.u8(IN_KIND_EDIT_UPDATE);
  w.u8(enumIndex(IN_UPDATE_KINDS, "lock"));
  w.u8(IN_LOCK_ATTR_PREVIEW);
  w.u8(0); // previewKind = port
  w.i32(nodeRow);
  w.str(portName);
  w.bool(isInput);
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
  w.i32(ev.hit.handholdTerm);
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
  u16(): number {
    const v = this.view.getUint16(this.pos, true);
    this.pos += 2;
    return v;
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
  | { kind: "play" | "pause" | "resend" | "save" | "fade-toggle" | "clear-rule" | "delete-selected-lock" }
  | { kind: "raw-input"; event: RawInputEvent }
  | { kind: "edit-create" | "edit-delete"; target: string; targetHandle: string }
  | { kind: "edit-update"; entity: "overlays"; attr: "toggle"; flag: OverlayFlag }
  | { kind: "edit-update"; entity: "lock"; attr: "active" | "selected"; index: number }
  | { kind: "edit-update"; entity: "lock"; attr: "author"; action: "begin"; eqKind: number }
  | { kind: "edit-update"; entity: "lock"; attr: "author"; action: "node" | "torus"; nodeRow: number }
  | {
      kind: "edit-update";
      entity: "lock";
      attr: "author";
      action: "latch";
      comp: number;
      sign: number;
    }
  | {
      kind: "edit-update";
      entity: "lock";
      attr: "author";
      action: "port";
      nodeRow: number;
      portName: string;
      isInput: boolean;
    }
  | {
      kind: "edit-update";
      entity: "lock";
      attr: "preview";
      nodeRow: number;
      portName?: string;
      isInput?: boolean;
    };

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
    case IN_KIND_SAVE:
      return { kind: "save" };
    case IN_KIND_FADE_TOGGLE:
      return { kind: "fade-toggle" };
    case IN_KIND_CLEAR_RULE:
      return { kind: "clear-rule" };
    case IN_KIND_DELETE_SELECTED_LOCK:
      return { kind: "delete-selected-lock" };
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
          handholdTerm: r.i32(),
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
      // entityKind byte selects overlays vs lock; attr byte + numeric payload follow.
      const entityKind = IN_UPDATE_KINDS[r.u8()];
      if (entityKind === "lock") {
        const attr = r.u8();
        if (attr === IN_LOCK_ATTR_ACTIVE || attr === IN_LOCK_ATTR_SELECTED) {
          const index = r.i32();
          return { kind: "edit-update", entity: "lock", attr: attr === IN_LOCK_ATTR_ACTIVE ? "active" : "selected", index };
        }
        if (attr === IN_LOCK_ATTR_AUTHOR) {
          const action = IN_AUTHOR_ACTIONS[r.u8()];
          switch (action) {
            case "begin":
              return { kind: "edit-update", entity: "lock", attr: "author", action: "begin", eqKind: r.u8() };
            case "node":
            case "torus":
              return { kind: "edit-update", entity: "lock", attr: "author", action, nodeRow: r.i32() };
            case "latch": {
              const comp = r.u8();
              const signBit = r.u8();
              return { kind: "edit-update", entity: "lock", attr: "author", action: "latch", comp, sign: signBit ? -1 : 1 };
            }
            case "port": {
              const nodeRow = r.i32();
              const portName = r.str();
              const isInput = r.bool();
              return { kind: "edit-update", entity: "lock", attr: "author", action: "port", nodeRow, portName, isInput };
            }
            default:
              return undefined;
          }
        }
        if (attr === IN_LOCK_ATTR_PREVIEW) {
          const previewKind = r.u8();
          const nodeRow = r.i32();
          if (previewKind === 0) {
            const portName = r.str();
            const isInput = r.bool();
            return { kind: "edit-update", entity: "lock", attr: "preview", nodeRow, portName, isInput };
          }
          return { kind: "edit-update", entity: "lock", attr: "preview", nodeRow };
        }
        return undefined;
      }
      const attr = r.u8();
      if (attr === IN_OVERLAY_ATTR_TOGGLE) {
        const flag = OVERLAY_FLAG_ORDER[r.u8()];
        if (!flag) return undefined;
        return { kind: "edit-update", entity: "overlays", attr: "toggle", flag };
      }
      return undefined;
    }
  }
  return undefined;
}
