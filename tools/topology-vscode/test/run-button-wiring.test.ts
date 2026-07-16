// run-button-wiring.test.ts — proves the RunButton's label derivation is actually WIRED
// to the real buffer readers, not just correct in isolation (that's run-button-state.test.ts).
//
// This replaces the jsdom + @testing-library/react component-render test (RunButton.test.tsx,
// removed — see CLAUDE.md workflow notes / task report on dependency cost). The React-rendering
// half of that test added no coverage beyond run-button-state.test.ts's pure derivation tests;
// the half that mattered was driving the REAL readers (readClockHaltedFlag / readClockHasRunOnce)
// against a REAL binary snapshot buffer, because the live bug that reached the user
// (fix(webview): RunButton reads "run" on first load, not "resume", fa633234) was a buffer-field
// misread: readClockHasRunOnce() read the header's frame-emit tick counter instead of the Clock
// block's dedicated HasRun column. Mocking the readers would have missed that bug entirely, so
// this test calls them for real (no mocks) and feeds their output into the real
// deriveRunButtonState.
//
// No jsdom, no React render — runs under vitest's plain `environment: "node"`.

import { describe, it, expect } from "vitest";
import { setLatestSnapshot } from "../src/webview/snapshot-buffer";
import { readClockHaltedFlag, readClockHasRunOnce } from "../src/webview/three/clock-state";
import { deriveRunButtonState } from "../src/webview/three/run-button-state";
import {
  BUF_HEADER_SIZE,
  CAMERA_STRIDE,
  OVERLAY_STRIDE,
  SCENE_STRIDE,
  CLOCK_STRIDE,
  CLOCK_COL_HALTED,
  CLOCK_COL_HAS_RUN,
} from "../src/schema/buffer-layout";

/** Build a minimal, well-formed snapshot ArrayBuffer (0 beads/nodes/edges/ports) with
 *  the Clock block set to the given (halted, hasRun) bits. Salvaged from the deleted
 *  test/RunButton.test.tsx's makeClockOnlySnapshot helper. Mirrors the layout
 *  decodeSnapshot expects (see buffer-decode.ts / buffer-decode.test.ts's makeSnapshot). */
function makeClockOnlySnapshot(halted: boolean, hasRun: boolean, tick = 1): ArrayBuffer {
  const totalBytes = BUF_HEADER_SIZE + CAMERA_STRIDE + OVERLAY_STRIDE + SCENE_STRIDE + CLOCK_STRIDE;
  const buf = new ArrayBuffer(totalBytes);
  const dv = new DataView(buf);
  // header: tick, beadCount, nodeCount, edgeCount, portCount, labelBytesCount, eventCount,
  // portNameBytesCount, edgeLabelBytesCount, layoutLinkCount — all zero except tick.
  dv.setUint32(0, tick, true); // tick
  const clockOff = BUF_HEADER_SIZE + CAMERA_STRIDE + OVERLAY_STRIDE + SCENE_STRIDE;
  dv.setUint8(clockOff + CLOCK_COL_HALTED, halted ? 1 : 0);
  dv.setUint8(clockOff + CLOCK_COL_HAS_RUN, hasRun ? 1 : 0);
  return buf;
}

describe("RunButton wiring: real readers + real buffer -> deriveRunButtonState", () => {
  it("not active -> '▶ run' / action 'run' (regardless of buffer content)", () => {
    setLatestSnapshot(makeClockOnlySnapshot(true, false));
    const halted = readClockHaltedFlag();
    const hasRun = readClockHasRunOnce();
    const state = deriveRunButtonState(false, halted, hasRun);
    expect(state.label).toBe("▶ run");
    expect(state.action).toBe("run");
  });

  it("active && !halted -> '⏸ pause' / action 'pause'", () => {
    setLatestSnapshot(makeClockOnlySnapshot(false, true));
    const halted = readClockHaltedFlag();
    const hasRun = readClockHasRunOnce();
    const state = deriveRunButtonState(true, halted, hasRun);
    expect(state.label).toBe("⏸ pause");
    expect(state.action).toBe("pause");
  });

  it("active && halted && !hasRun -> '▶ run' / action 'run' (first load, tick already > 0 from startup geometry)", () => {
    // tick=3 simulates startup-geometry frames already emitted while still halted — the
    // exact shape of the shipped bug (reading tick>0 as "has run once").
    setLatestSnapshot(makeClockOnlySnapshot(true, false, 3));
    const halted = readClockHaltedFlag();
    const hasRun = readClockHasRunOnce();
    const state = deriveRunButtonState(true, halted, hasRun);
    expect(state.label).toBe("▶ run");
    expect(state.action).toBe("run");
  });

  it("active && halted && hasRun -> '▶ resume' / action 'run'", () => {
    setLatestSnapshot(makeClockOnlySnapshot(true, true));
    const halted = readClockHaltedFlag();
    const hasRun = readClockHasRunOnce();
    const state = deriveRunButtonState(true, halted, hasRun);
    expect(state.label).toBe("▶ resume");
    expect(state.action).toBe("run");
  });
});
