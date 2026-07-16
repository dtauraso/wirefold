// Unit tests for run-button-state.ts's deriveRunButtonState — the pure derivation of
// the RunButton's THREE labels (▶ run / ⏸ pause / ▶ resume) and click action from facts
// Go already owns: isActive (ext-host process liveness), clockHalted (Go's Clock-block
// truth), and hasRunOnce (Go's header tick > 0 at some point this process).
//
// This is a plain-function unit test rather than a component-render test: this repo has
// no React/component-test harness (no @testing-library, no jsdom vitest environment —
// see vitest.config.mts's `environment: "node"`), and RunButton.tsx itself cannot import
// cleanly under that config (react-dom + vscode-api touch `window`/`acquireVsCodeApi` at
// module load). Extracting the derivation into run-button-state.ts (no such side effects)
// is the smaller, harness-free alternative the task allowed.

import { describe, it, expect } from "vitest";
import { deriveRunButtonState } from "../src/webview/three/run-button-state";

describe("deriveRunButtonState", () => {
  it("not active -> ▶ run / action run", () => {
    // process never spawned (or was stopped): isActive false regardless of clockHalted/tick.
    expect(deriveRunButtonState(false, null, null)).toEqual({
      label: "▶ run",
      action: "run",
      isRunning: false,
    });
  });

  it("active and unhalted (running) -> ⏸ pause / action pause", () => {
    expect(deriveRunButtonState(true, false, true)).toEqual({
      label: "⏸ pause",
      action: "pause",
      isRunning: true,
    });
  });

  it("active, halted, never run (tick still 0) -> ▶ run / action run", () => {
    expect(deriveRunButtonState(true, true, false)).toEqual({
      label: "▶ run",
      action: "run",
      isRunning: false,
    });
  });

  it("active, halted, has run before (tick > 0) -> ▶ resume / action run", () => {
    expect(deriveRunButtonState(true, true, true)).toEqual({
      label: "▶ resume",
      action: "run",
      isRunning: false,
    });
  });

  it("run and resume post the IDENTICAL action — Go has one Resume(), no 'resume' wire kind", () => {
    const freshRun = deriveRunButtonState(true, true, false);
    const resume = deriveRunButtonState(true, true, true);
    expect(freshRun.action).toBe("run");
    expect(resume.action).toBe("run");
    expect(freshRun.action).toBe(resume.action);
  });
});
