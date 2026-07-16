// run-button-state.ts — pure derivation of the RunButton's label + click action.
//
// Split out of RunButton.tsx so it can be unit-tested without a component-render harness
// (this repo has none — see CLAUDE.md workflow notes / task report). RunButton.tsx itself
// pulls in react-dom + vscode-api, which touch `window`/`acquireVsCodeApi` at module load
// and cannot import cleanly under vitest's `environment: "node"` config. This module has
// no such side effects: it's plain data in, plain data out.
//
// Derived from facts Go already owns:
//   - isActive: a Go process is spawned (ext-host process liveness; a dead process
//     cannot report itself dead).
//   - clockHalted: Go's Clock-block truth, streamed in the buffer — NOT predicted from
//     the stdin play/pause write.
//   - hasRunOnce: Go's header tick > 0 at some point this process — Go's clock only
//     advances tick while unhalted (RealClock.activeElapsedLocked), so tick > 0 means
//     the clock has run before. This is what distinguishes "▶ run" (never started, or
//     process not spawned) from "▶ resume" (paused after having run) — WITHOUT a new
//     buffer column or any TS-authored "hasEverRun" flag: it's read from the buffer
//     every time via useClockHasRunOnce (clock-state.ts).
//
// The click ACTION is identical for "run" and "resume": both post {type:"run"}. Go's
// gate has ONE Resume() (see stdin_reader.go handlePlayMsg); there is no separate
// "resume" message kind on the wire (deleted in 9bdfdca1 — do not reintroduce it). This
// is a label-only distinction.

export type RunButtonState = {
  label: "▶ run" | "⏸ pause" | "▶ resume";
  action: "run" | "pause";
  isRunning: boolean;
};

export function deriveRunButtonState(
  isActive: boolean,
  clockHalted: boolean | null,
  hasRunOnce: boolean | null,
): RunButtonState {
  const isRunning = isActive && clockHalted === false;
  if (isRunning) return { label: "⏸ pause", action: "pause", isRunning: true };
  if (isActive && clockHalted === true && hasRunOnce === true) {
    return { label: "▶ resume", action: "run", isRunning: false };
  }
  return { label: "▶ run", action: "run", isRunning: false };
}
