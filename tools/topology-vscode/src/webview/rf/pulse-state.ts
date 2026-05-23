// Imperative bridge + context for pulse animation state.
// pump.ts calls setPulse / clearPulse; App registers the React setter
// on mount so the context re-renders when the map changes.
//
// Key: edge id.
// Value: { value, simStep } describing the in-flight pulse.

import { createContext, useContext } from "react";

export interface PulseData {
  value: number;
  simStep: number;
}

export type PulseMap = ReadonlyMap<string, PulseData>;

type Setter = (next: Map<string, PulseData>) => void;

let _setter: Setter | null = null;
let _current: Map<string, PulseData> = new Map();

export function registerPulseSetter(setter: Setter) {
  _setter = setter;
}

export function setPulse(edgeId: string, data: PulseData) {
  const next = new Map(_current);
  next.set(edgeId, data);
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

export const PulseCtx = createContext<PulseMap>(new Map());

export function usePulseCtx(): PulseMap {
  return useContext(PulseCtx);
}
