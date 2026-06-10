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
// Key: edge id. Value: { value, simStep, target, targetHandle, pos }.
//   pos is the latest Go-supplied world position, or null until the first
//   position event arrives (PulseBead stays hidden while pos is null).
//
// The wire-tube curve is NOT here: it is Go-authoritative and lives in
// edge-geometry.ts (Phase 3), written by pump.ts from Go's geometry stream.

import { postLog } from "../log/post";

export interface PulseData {
  value: number;
  simStep: number;
  target: string;
  targetHandle: string;
  /** Go-computed bead world position (Phase 2 position stream); null until the
   *  first position event for this pulse arrives. TS never computes this. */
  pos: { x: number; y: number; z: number } | null;
}

export type PulseMap = ReadonlyMap<string, PulseData>;

let _current: Map<string, PulseData> = new Map();

export function setPulse(edgeId: string, data: Omit<PulseData, "pos">) {
  // Records the in-flight bead and its routing identity (target/targetHandle) on
  // the send event. The position is filled in by setPulsePos as Go's stream
  // arrives; until then pos stays null and PulseBead stays hidden.
  const next = new Map(_current);
  next.set(edgeId, { ...data, pos: null });
  _current = next;
}

/** Set the bead's Go-computed world position from a position trace event.
 *  Called ~16 ms; mutates in place (no React commit). If a position arrives
 *  before the send event created the entry, it is dropped — the send event is
 *  what establishes routing identity. */
export function setPulsePos(edgeId: string, x: number, y: number, z: number) {
  const existing = _current.get(edgeId);
  if (!existing) return;
  existing.pos = { x, y, z };
}

export function clearPulse(edgeId: string) {
  const keysBefore = [..._current.keys()];
  if (!_current.has(edgeId)) {
    // Asked to clear a bead that isn't in-flight — log so a delete capture
    // shows we did NOT drop a bead (and which keys were present instead).
    postLog("clearPulse", { edgeId, removed: false, keysBefore });
    return;
  }
  const next = new Map(_current);
  next.delete(edgeId);
  _current = next;
  postLog("clearPulse", { edgeId, removed: true, keysBefore, keysAfter: [...next.keys()] });
}

export function getPulseMap(): PulseMap {
  return _current;
}

// The former non-React edge-curve cache (TS-built) was removed in Phase 3: the edge
// curve is Go-authoritative and lives in edge-geometry.ts, written by pump.ts from
// Go's geometry stream. This file holds bead positions only.

