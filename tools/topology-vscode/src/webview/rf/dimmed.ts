// Imperative bridge + context for dimmed.
// main.tsx calls setDimmedImperative; App registers the React setter on mount.

import { createContext, useContext } from "react";

type Setter = (next: Set<string> | null) => void;

let _setter: Setter | null = null;
let _current: Set<string> | null = null;

export function registerDimmedSetter(setter: Setter) {
  _setter = setter;
}

export function setDimmedImperative(next: Set<string> | null) {
  _current = next;
  _setter?.(next);
}

export function getDimmed(): Set<string> | null {
  return _current;
}

export const DimmedCtx = createContext<Set<string> | null>(null);

export function useDimmedCtx(): Set<string> | null {
  return useContext(DimmedCtx);
}
