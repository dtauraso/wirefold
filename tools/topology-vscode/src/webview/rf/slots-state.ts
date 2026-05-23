// Imperative bridge + context for node slot state.
// pump.ts calls setSlots; App registers the React setter
// on mount so the context re-renders when the map changes.
//
// Key: node id.
// Value: SlotMap describing the current slot phase+value for each port.

import { createContext, useContext } from "react";
import type { SlotMap } from "../../messages";

export type SlotsMap = ReadonlyMap<string, SlotMap>;

type Setter = (next: Map<string, SlotMap>) => void;

let _setter: Setter | null = null;
let _current: Map<string, SlotMap> = new Map();

export function registerSlotsSetter(setter: Setter) {
  _setter = setter;
}

export function setSlots(nodeId: string, slots: SlotMap) {
  const next = new Map(_current);
  next.set(nodeId, slots);
  _current = next;
  _setter?.(next);
}

export function getSlotsMap(): SlotsMap {
  return _current;
}

export const SlotsCtx = createContext<SlotsMap>(new Map());

export function useSlotsCtx(): SlotsMap {
  return useContext(SlotsCtx);
}
