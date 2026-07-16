// @vitest-environment jsdom
//
// RunButton.tsx render tests — the component that shipped the two live bugs
// this suite exists to close:
//   1. run() silently no-op'd on a remounted webview because run-status stayed stale
//      ("active") while the process was actually dead — the button went inert.
//   2. hasRunOnce was misread from a frame-emit counter, so the label showed
//      "▶ resume" on first load instead of "▶ run".
//
// Drives the REAL component through react-dom/test-utils via @testing-library/react,
// feeding a REAL binary snapshot buffer through setLatestSnapshot (not mocked hooks) so
// a wrong buffer-field read (bug #2's shape) is caught, not just the pure derivation
// (already covered by run-button-state.test.ts).

import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { render, cleanup, fireEvent } from "@testing-library/react";
import { RunStatusCtx, type RunStatusUI } from "../src/webview/state/run-status";
import { setLatestSnapshot } from "../src/webview/snapshot-buffer";
import {
  BUF_HEADER_SIZE,
  CAMERA_STRIDE,
  OVERLAY_STRIDE,
  SCENE_STRIDE,
  CLOCK_STRIDE,
  CLOCK_COL_HALTED,
  CLOCK_COL_HAS_RUN,
} from "../src/schema/buffer-layout";

// vscode-api.ts calls acquireVsCodeApi() at module load time; that global only exists
// inside a real VS Code webview host. Stub it before any module that imports vscode-api
// (RunButton -> vscode-api) is loaded.
const postMessage = vi.fn();
(globalThis as unknown as { acquireVsCodeApi: () => unknown }).acquireVsCodeApi = () => ({
  postMessage,
  setState: () => {},
  getState: () => ({}),
});

// Imported AFTER the acquireVsCodeApi stub is installed above.
const { RunButton } = await import("../src/webview/three/RunButton");

/** Build a minimal, well-formed snapshot ArrayBuffer (0 beads/nodes/edges/ports) with
 *  the Clock block set to the given (halted, hasRun) bits. Mirrors the layout
 *  decodeSnapshot expects (see buffer-decode.ts / buffer-decode.test.ts's makeSnapshot). */
function makeClockOnlySnapshot(halted: boolean, hasRun: boolean): ArrayBuffer {
  const totalBytes = BUF_HEADER_SIZE + CAMERA_STRIDE + OVERLAY_STRIDE + SCENE_STRIDE + CLOCK_STRIDE;
  const buf = new ArrayBuffer(totalBytes);
  const dv = new DataView(buf);
  // header: tick, beadCount, nodeCount, edgeCount, portCount, labelBytesCount, eventCount,
  // portNameBytesCount, edgeLabelBytesCount, layoutLinkCount — all zero except tick.
  dv.setUint32(0, 1, true); // tick
  const clockOff = BUF_HEADER_SIZE + CAMERA_STRIDE + OVERLAY_STRIDE + SCENE_STRIDE;
  dv.setUint8(clockOff + CLOCK_COL_HALTED, halted ? 1 : 0);
  dv.setUint8(clockOff + CLOCK_COL_HAS_RUN, hasRun ? 1 : 0);
  return buf;
}

function renderRunButton(status: RunStatusUI) {
  return render(
    <RunStatusCtx.Provider value={status}>
      <RunButton />
    </RunStatusCtx.Provider>,
  );
}

const ACTIVE: RunStatusUI = { state: "active" };
const IDLE: RunStatusUI = { state: "idle" };

beforeEach(() => {
  document.body.innerHTML = '<div id="run-mount"></div>';
  postMessage.mockClear();
});

afterEach(() => {
  cleanup();
});

describe("RunButton — four states", () => {
  it("not active: label '▶ run', stop disabled", () => {
    setLatestSnapshot(makeClockOnlySnapshot(true, false));
    const { getByText } = renderRunButton(IDLE);
    expect(getByText("▶ run")).toBeTruthy();
    const stop = getByText("■ stop") as HTMLButtonElement;
    expect(stop.disabled).toBe(true);
  });

  it("active && clock ticking: label '⏸ pause', click posts pause, stop enabled", () => {
    setLatestSnapshot(makeClockOnlySnapshot(false, true));
    const { getByText } = renderRunButton(ACTIVE);
    const runBtn = getByText("⏸ pause") as HTMLButtonElement;
    const stop = getByText("■ stop") as HTMLButtonElement;
    expect(stop.disabled).toBe(false);
    fireEvent.click(runBtn);
    expect(postMessage).toHaveBeenCalledWith({ type: "pause" });
  });

  it("active && halted && !hasRun: label '▶ run', click posts run, stop enabled", () => {
    setLatestSnapshot(makeClockOnlySnapshot(true, false));
    const { getByText } = renderRunButton(ACTIVE);
    const runBtn = getByText("▶ run") as HTMLButtonElement;
    const stop = getByText("■ stop") as HTMLButtonElement;
    expect(stop.disabled).toBe(false);
    fireEvent.click(runBtn);
    expect(postMessage).toHaveBeenCalledWith({ type: "run" });
  });

  it("active && halted && hasRun: label '▶ resume', click posts run, stop enabled", () => {
    setLatestSnapshot(makeClockOnlySnapshot(true, true));
    const { getByText } = renderRunButton(ACTIVE);
    const runBtn = getByText("▶ resume") as HTMLButtonElement;
    const stop = getByText("■ stop") as HTMLButtonElement;
    expect(stop.disabled).toBe(false);
    fireEvent.click(runBtn);
    expect(postMessage).toHaveBeenCalledWith({ type: "run" });
  });

  it("clicking stop posts {type:'stop'}", () => {
    setLatestSnapshot(makeClockOnlySnapshot(false, true));
    const { getByText } = renderRunButton(ACTIVE);
    fireEvent.click(getByText("■ stop"));
    expect(postMessage).toHaveBeenCalledWith({ type: "stop" });
  });
});

describe("RunButton — regression coverage for the two shipped bugs", () => {
  // Bug #1: run status stayed "active" on a remounted webview even though the
  // underlying Go process was gone / clock never ran. This asserts the button
  // renders in the CORRECT inert-ish state (disabled stop button reflects a truly
  // dead process; a live-but-never-run process shows an enabled, clickable "run").
  // The regression this guards is: a stale/absent status must NOT silently produce a
  // dead button — either it's genuinely not active (stop disabled, run clickable), or
  // it's active and the run control must be clickable.
  it("stale/absent active status (not active) renders an inert stop button, but run stays clickable", () => {
    setLatestSnapshot(makeClockOnlySnapshot(true, false));
    const { getByText } = renderRunButton(IDLE);
    const runBtn = getByText("▶ run") as HTMLButtonElement;
    const stop = getByText("■ stop") as HTMLButtonElement;
    expect(stop.disabled).toBe(true);
    expect(runBtn.disabled).toBe(false);
    fireEvent.click(runBtn);
    expect(postMessage).toHaveBeenCalledWith({ type: "run" });
  });

  // Bug #2: hasRunOnce was misread from the frame-emit tick (nonzero on the very
  // first frame, since startup geometry emits while still halted) instead of the
  // Clock block's dedicated HasRun column. On first load, tick is already > 0
  // (startup geometry frame) while HasRun is still 0 — the correct read must ignore
  // tick and show "▶ run", not "▶ resume".
  it("first-load snapshot (tick > 0 from startup geometry, HasRun still 0) shows '▶ run' not '▶ resume'", () => {
    const buf = makeClockOnlySnapshot(true, false);
    new DataView(buf).setUint32(0, 3, true); // simulate startup-geometry tick already > 0
    setLatestSnapshot(buf);
    const { getByText, queryByText } = renderRunButton(ACTIVE);
    expect(getByText("▶ run")).toBeTruthy();
    expect(queryByText("▶ resume")).toBeNull();
  });
});
