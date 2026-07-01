// Imperative pulse-position store. Phase 2: Go's per-frame position stream is the
// sole source of bead positions — TS plots, it computes no geometry. pump.ts
// calls setPulse (on send, to record the in-flight bead + its routing identity),
// setPulsePos (on each ~16 ms position event, to set the Go-computed world
// position), and clearPulse (on done). PulseBead reads getPulseMap() imperatively
// each frame and draws pulse.pos directly — no curve sampling, no t, no clock.
//
// This is a plain non-React Map: there is no React subscriber (PulseBead polls in
// useFrame), so updates mutate the map without triggering a commit. That keeps the
// 60 Hz position stream off React's render path entirely.
//
// Key: edge id. Value: { value, simStep, target, targetHandle, pos, frac }.
//   pos is the latest Go-supplied world position; frac is the bead's Go-owned
//   fractional progress t (0..1). Both null until the first position event arrives
//   (PulseBead stays hidden while frac is null). PulseBead places the bead at
//   lerp(liveStart, liveEnd, frac) on the editor's LOCAL node port positions.
//
// The wire-tube curve is NOT here: it is Go-authoritative and lives in
// edge-geometry.ts (Phase 3), written by pump.ts from Go's geometry stream.

import { postLog } from "../log/post";

export interface PulseData {
  /** The edge this bead is traversing. The map key is `${edgeId}:${beadID}` so a
   *  wire can hold N beads at once (a clock-paced train); renderers read this to
   *  find the edge segment they ride. */
  edgeId: string;
  value: number;
  /** Go-computed bead world position (Phase 2 position stream). The slot is only
   *  created from a position event, so this is always set. TS never computes it. */
  pos: { x: number; y: number; z: number };
  /** Go-owned FRACTIONAL progress t (0..1) of the bead along its wire, from the
   *  position event. The editor places the bead at lerp(liveStart, liveEnd, frac)
   *  on its LOCAL (dragged) node port positions so the bead rides the live wire
   *  with no round-trip lag. Always set (slot established from a position event). */
  frac: number;
}

export type PulseMap = ReadonlyMap<string, PulseData>;

let _current: Map<string, PulseData> = new Map();

// Secondary index: edgeId → (composite key → PulseData), maintained on every
// write/delete alongside _current. PulseBead (one per wire) reads only its own
// edge's slice, so per-frame rendering is O(beads-on-this-edge) instead of
// O(all-beads-across-all-edges). The PulseData objects are shared with _current
// (same reference), so in-place mutation in setPulsePos is visible through both.
const _byEdge: Map<string, Map<string, PulseData>> = new Map();

function indexPut(key: string, data: PulseData): void {
  let inner = _byEdge.get(data.edgeId);
  if (!inner) {
    inner = new Map();
    _byEdge.set(data.edgeId, inner);
  }
  inner.set(key, data);
}

function indexDelete(key: string, edgeId: string): void {
  const inner = _byEdge.get(edgeId);
  if (!inner) return;
  inner.delete(key);
  if (inner.size === 0) _byEdge.delete(edgeId);
}

const _EMPTY_SLICE: ReadonlyMap<string, PulseData> = new Map();

/** Composite map key: a wire (edgeId) may carry N beads at once, each with a
 *  distinct per-wire id (beadID = Go's gen). Keying by edge alone collapsed N
 *  beads to one sprite; keying by `${edgeId}:${beadID}` lets them coexist. */
export function pulseKey(edgeId: string, beadID: number): string {
  return `${edgeId}:${beadID}`;
}

/** Set/refresh one bead's Go-computed world position + fraction from a position
 *  (edge-bead) trace event. The position stream is the per-bead ESTABLISHER:
 *  with a clock-paced train one send fires but N beads stream their own
 *  positions, so the first position event for a (edge, bead) pair creates the
 *  slot (carrying its value). Called ~16 ms; mutates in place (no React commit). */
export function setPulsePos(edgeId: string, beadID: number, value: number, x: number, y: number, z: number, frac: number) {
  const key = pulseKey(edgeId, beadID);
  const existing = _current.get(key);
  if (existing) {
    existing.value = value;
    existing.pos = { x, y, z };
    existing.frac = frac;
    return;
  }
  // First position for this bead — establish its slot (new Map so a future
  // structural change is observable; per-frame pos updates mutate in place).
  const data: PulseData = { edgeId, value, pos: { x, y, z }, frac };
  const next = new Map(_current);
  next.set(key, data);
  _current = next;
  indexPut(key, data);
}

export function clearPulse(edgeId: string, beadID: number) {
  const key = pulseKey(edgeId, beadID);
  const keysBefore = [..._current.keys()];
  if (!_current.has(key)) {
    // Asked to clear a bead that isn't in-flight — log so a delete capture
    // shows we did NOT drop a bead (and which keys were present instead).
    postLog("clearPulse", { edgeId, beadID, removed: false, keysBefore });
    return;
  }
  const next = new Map(_current);
  next.delete(key);
  _current = next;
  indexDelete(key, edgeId);
  postLog("clearPulse", { edgeId, beadID, removed: true, keysBefore, keysAfter: [...next.keys()] });
}

/** Drop EVERY in-flight bead on one edge (all `${edgeId}:*` entries). Used by
 *  edge-level actions (delete edge, fade edge) where the whole wire's beads must
 *  vanish at once — distinct from the pump's per-bead clearPulse (arrive/cancel). */
export function clearPulsesForEdge(edgeId: string) {
  let removed = 0;
  const next = new Map(_current);
  for (const [key, e] of _current) {
    if (e.edgeId === edgeId) {
      next.delete(key);
      removed++;
    }
  }
  if (removed > 0) _current = next;
  _byEdge.delete(edgeId);
  postLog("clearPulsesForEdge", { edgeId, removed });
}

/** Wipe every in-flight bead. Called at run-start (store.load) so a fresh run's
 *  process (zero in-flight beads in Go) does not inherit a zombie bead left in
 *  the store from a prior run that was stopped after "send" but before "arrive".
 *  Mirrors clearPulse: swaps _current for a fresh Map — PulseBead polls getPulseMap
 *  in useFrame, so the next frame draws no beads (no version counter/listeners here). */
export function clearAllPulses() {
  const count = _current.size;
  _current = new Map();
  _byEdge.clear();
  postLog("lifecycle", { phase: "pulse-reset", cleared: count });
}

export function getPulseMap(): PulseMap {
  return _current;
}

/** Per-edge slice of the pulse map: only the in-flight beads on `edgeId`. Lets
 *  each PulseBead render in O(beads-on-this-edge) instead of scanning the whole
 *  flat map every frame. Returns an empty map when the edge has no live beads. */
export function getPulseMapForEdge(edgeId: string): ReadonlyMap<string, PulseData> {
  return _byEdge.get(edgeId) ?? _EMPTY_SLICE;
}

// The former non-React edge-curve cache (TS-built) was removed in Phase 3: the edge
// curve is Go-authoritative and lives in edge-geometry.ts, written by pump.ts from
// Go's geometry stream. This file holds bead positions only.

