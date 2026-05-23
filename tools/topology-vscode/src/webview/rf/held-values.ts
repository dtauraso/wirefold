// Imperative bridge + context for held values.
// pump.ts calls setHeldValue / clearHeldValue; App registers the React setter
// on mount so the context re-renders when the map changes.
//
// Key: `${nodeId}:${port}` (destination node id + input port name).
// Value: the value that is in-transit / held at the input port.

import { createContext, useContext } from "react";

export type HeldValues = ReadonlyMap<string, unknown>;

type Setter = (next: Map<string, unknown>) => void;

let _setter: Setter | null = null;
let _current: Map<string, unknown> = new Map();

export function registerHeldValuesSetter(setter: Setter) {
  _setter = setter;
}

export function setHeldValue(nodeId: string, port: string, value: unknown) {
  const next = new Map(_current);
  next.set(`${nodeId}:${port}`, value);
  _current = next;
  _setter?.(next);
}

export function clearHeldValue(nodeId: string, port: string) {
  if (!_current.has(`${nodeId}:${port}`)) return;
  const next = new Map(_current);
  next.delete(`${nodeId}:${port}`);
  _current = next;
  _setter?.(next);
}

export function getHeldValues(): HeldValues {
  return _current;
}

export const HeldValuesCtx = createContext<HeldValues>(new Map());

export function useHeldValuesCtx(): HeldValues {
  return useContext(HeldValuesCtx);
}
