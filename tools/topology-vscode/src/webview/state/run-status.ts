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

// Pause-time accounting: track how long the sim has been paused so that
// animation clocks can be adjusted to freeze during pause.
let _pauseStartedAt: number | null = null;
let _pauseAccumulatedMs: number = 0;

/**
 * Returns a "sim time" that freezes while the run is paused and resumes after.
 * Use in place of performance.now() wherever animation positions are computed.
 */
export function getPauseAdjustedNow(): number {
  const pausedContribution =
    _pauseStartedAt !== null ? performance.now() - _pauseStartedAt : 0;
  return performance.now() - _pauseAccumulatedMs - pausedContribution;
}

export function registerRunStatusSetter(setter: Setter) {
  _setter = setter;
}

export function setRunStatusImperative(next: RunStatusUI) {
  const prev = _current;
  _current = next;

  const waspaused = prev.state === "paused";
  const isPaused = next.state === "paused";

  if (!waspaused && isPaused) {
    // Transitioning INTO paused.
    _pauseStartedAt = performance.now();
  } else if (waspaused && !isPaused) {
    // Transitioning OUT of paused.
    if (_pauseStartedAt !== null) {
      _pauseAccumulatedMs += performance.now() - _pauseStartedAt;
      _pauseStartedAt = null;
    }
  }

  _setter?.(next);
}

export function getRunStatus(): RunStatusUI {
  return _current;
}

export const RunStatusCtx = createContext<RunStatusUI>({ state: "idle" });

export function useRunStatusCtx(): RunStatusUI {
  return useContext(RunStatusCtx);
}
