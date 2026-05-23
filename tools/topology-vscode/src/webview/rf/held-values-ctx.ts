// Context + hook for held-values display — mirrors dimmed-ctx.ts pattern.
// Provides a Map<`${nodeId}:${port}`, value> to node components.
// GenericNode calls useHeldValuesCtx() then looks up its own nodeId+port.

import { createContext, useContext } from "react";
import type { HeldValues } from "./held-values-state";

export const HeldValuesCtx = createContext<HeldValues>(new Map());

export function useHeldValuesCtx(): HeldValues {
  return useContext(HeldValuesCtx);
}
