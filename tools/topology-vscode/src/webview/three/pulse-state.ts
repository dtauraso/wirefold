// Imperative bridge + context for pulse animation state.
// Also houses the edge-curve store: a non-React Map<edgeId, curve> that is
// populated synchronously in moveNode (same tick as position store update)
// so PulseBead always reads the current-frame curve without a React-commit lag.
// pump.ts calls setPulse / clearPulse; App registers the React setter
// on mount so the context re-renders when the map changes.
//
// Key: edge id.
// Value: { value, simStep, target, targetHandle, startTime } describing the in-flight pulse.
// startTime is performance.now() at the moment setPulse is called; it
// lets remounted components resume animation at the correct t rather
// than restarting from 0.

import * as THREE from "three";
import { getPauseAdjustedNow } from "../state/run-status";

export interface PulseData {
  value: number;
  simStep: number;
  target: string;
  targetHandle: string;
  simLatencyMs: number;
  startTime: number;
}

export type PulseMap = ReadonlyMap<string, PulseData>;

type Setter = (next: Map<string, PulseData>) => void;

let _setter: Setter | null = null;
let _current: Map<string, PulseData> = new Map();
// Guard: track which startTime we already posted "delivered" for, per edge.
const _deliveredAt: Map<string, number> = new Map();

export function registerPulseSetter(setter: Setter) {
  _setter = setter;
}

export function setPulse(edgeId: string, data: Omit<PulseData, "startTime">) {
  // data must include simLatencyMs (substrate-supplied duration) so PulseBead
  // computes t from substrate truth rather than a fabricated speed constant.
  // data must include target + targetHandle so use-pulse-animation can write
  // the held-value badge at t=1 (pulse arrival) rather than at send time.
  const next = new Map(_current);
  next.set(edgeId, { ...data, startTime: getPauseAdjustedNow() });
  _current = next;
  _setter?.(next);
}

/** Returns true if this is the first "delivered" post for this pulse instance. */
export function claimDelivered(edgeId: string, startTime: number): boolean {
  if (_deliveredAt.get(edgeId) === startTime) return false;
  _deliveredAt.set(edgeId, startTime);
  return true;
}

/** Overwrite an existing in-flight pulse with an explicit startTime.
 *  Used by the latency-changed handler to preserve the bead's visual
 *  progress fraction when wire geometry changes during a node drag. */
export function patchPulse(edgeId: string, simLatencyMs: number, startTime: number) {
  const existing = _current.get(edgeId);
  if (!existing) return;
  const next = new Map(_current);
  next.set(edgeId, { ...existing, simLatencyMs, startTime });
  _current = next;
  _setter?.(next);
}

export function clearPulse(edgeId: string) {
  if (!_current.has(edgeId)) return;
  const next = new Map(_current);
  next.delete(edgeId);
  _current = next;
  _setter?.(next);
}

export function getPulseMap(): PulseMap {
  return _current;
}

// ---------------------------------------------------------------------------
// Edge curve store — non-React, keyed by edgeId.
// Populated synchronously in moveNode + on load/createEdge so PulseBead
// always reads the up-to-date curve in the same useFrame tick.
// ---------------------------------------------------------------------------

const _curveMap: Map<string, THREE.QuadraticBezierCurve3> = new Map();

export function getCurve(edgeId: string): THREE.QuadraticBezierCurve3 | undefined {
  return _curveMap.get(edgeId);
}

export function setCurve(edgeId: string, curve: THREE.QuadraticBezierCurve3): void {
  _curveMap.set(edgeId, curve);
}

export function deleteCurve(edgeId: string): void {
  _curveMap.delete(edgeId);
}

