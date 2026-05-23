// Imperative bridge + context for node fire-flash state.
// pump.ts calls setLastFire; App registers the React setter
// on mount so the context re-renders when the map changes.
//
// Key: node id.
// Value: simStep number at which the node last fired.

import { createContext, useContext } from "react";

export type LastFireMap = ReadonlyMap<string, number>;

type Setter = (next: Map<string, number>) => void;

let _setter: Setter | null = null;
let _current: Map<string, number> = new Map();

export function registerLastFireSetter(setter: Setter) {
  _setter = setter;
}

export function setLastFire(nodeId: string, step: number) {
  const next = new Map(_current);
  next.set(nodeId, step);
  _current = next;
  _setter?.(next);
}

export function getLastFireMap(): LastFireMap {
  return _current;
}

export const LastFireCtx = createContext<LastFireMap>(new Map());

export function useLastFireCtx(): LastFireMap {
  return useContext(LastFireCtx);
}
