// Imperative bridge + context for runStatus.
// main.tsx calls setRunStatusImperative from the window message handler;
// App registers the React setState via registerRunStatusSetter on mount.

import { createContext, useContext } from "react";
import type { RunStatus } from "../../messages";

// Webview-local: idle is the pre-first-run default. The wire RunStatus has
// no idle variant (the host only emits running/paused/ok/error/cancelled).
export type RunStatusUI = RunStatus | { state: "idle" };

type Setter = (next: RunStatusUI) => void;

let _setter: Setter | null = null;
let _current: RunStatusUI = { state: "idle" };

export function registerRunStatusSetter(setter: Setter) {
  _setter = setter;
}

export function setRunStatusImperative(next: RunStatusUI) {
  _current = next;
  _setter?.(next);
}

export function getRunStatus(): RunStatusUI {
  return _current;
}

export const RunStatusCtx = createContext<RunStatusUI>({ state: "idle" });

export function useRunStatusCtx(): RunStatusUI {
  return useContext(RunStatusCtx);
}
