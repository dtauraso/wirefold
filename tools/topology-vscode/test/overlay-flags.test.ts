// Unit tests for overlay-flags.ts — the row-keyed READ resource over the buffer's
// Overlay columns used by the new-system overlay toggle control + NavGuides gating.
//
// Asserts:
//   - readOverlayFlags decodes the Overlay row into store-polarity booleans, with the
//     labelsGlobal/badgesGlobal HIDDEN-sense inversion (buffer stores VISIBLE).
//   - a toggle round-trip is visible: mutating the buffer's Overlay column and pushing a
//     new snapshot changes the decoded value (Go flips → buffer column → control state).
//   - null when no snapshot has landed.
//   - stable object identity while the flags are unchanged (so useSyncExternalStore does
//     not re-render every 60fps snapshot).

import { describe, it, expect, beforeEach } from "vitest";
import { readOverlayFlags } from "../src/webview/three/overlay-flags";
import { setLatestSnapshot } from "../src/webview/snapshot-buffer";
import {
  BUF_HEADER_SIZE, NODE_STRIDE, INTERIOR_STRIDE, CAMERA_STRIDE, OVERLAY_STRIDE, RULE_BUILDER_STRIDE,
  OVERLAY_COL_SCENE_TORI, OVERLAY_COL_SCENE_POLES, OVERLAY_COL_NODE_POLES,
  OVERLAY_COL_ANGLE_LABELS, OVERLAY_COL_SEL_SPHERE_POLES, OVERLAY_COL_HANDHOLDS,
  OVERLAY_COL_LABELS_GLOBAL, OVERLAY_COL_BADGES_GLOBAL, OVERLAY_COL_OVERLAYS_VIS,
} from "../src/schema/buffer-layout";

// Build a node-less snapshot (0 beads/nodes/edges) carrying only the Camera + Overlay
// singletons. `set` writes an overlay column (u8) by offset.
function makeOverlaySnapshot(cols: Partial<Record<number, number>>): ArrayBuffer {
  // 0 nodes → 0 node bytes, 0 interior bytes, 0 edge bytes, 0 bead bytes.
  void NODE_STRIDE; void INTERIOR_STRIDE;
  const total = BUF_HEADER_SIZE + CAMERA_STRIDE + OVERLAY_STRIDE + RULE_BUILDER_STRIDE;
  const buf = new ArrayBuffer(total);
  const dv = new DataView(buf);
  // header counts all zero (0 beads/nodes/edges) — default.
  const overlayOff = BUF_HEADER_SIZE + CAMERA_STRIDE;
  for (const [col, val] of Object.entries(cols)) {
    dv.setUint8(overlayOff + Number(col), val ?? 0);
  }
  return buf;
}

describe("overlay-flags readOverlayFlags", () => {
  beforeEach(() => {
    // Reset the module cell to a known "all-visible" baseline between tests. (There is no
    // clear API — a fresh snapshot resets the cached bits.)
    setLatestSnapshot(makeOverlaySnapshot({
      [OVERLAY_COL_SCENE_TORI]: 1,
      [OVERLAY_COL_SCENE_POLES]: 1,
      [OVERLAY_COL_NODE_POLES]: 1,
      [OVERLAY_COL_ANGLE_LABELS]: 1,
      [OVERLAY_COL_SEL_SPHERE_POLES]: 1,
      [OVERLAY_COL_HANDHOLDS]: 1,
      [OVERLAY_COL_LABELS_GLOBAL]: 1,
      [OVERLAY_COL_BADGES_GLOBAL]: 1,
      [OVERLAY_COL_OVERLAYS_VIS]: 1,
    }));
  });

  it("decodes visible-sense columns to store-polarity booleans", () => {
    const f = readOverlayFlags();
    expect(f).not.toBeNull();
    expect(f!.tori).toBe(true);
    expect(f!.scenePoles).toBe(true);
    expect(f!.nodePoles).toBe(true);
    expect(f!.angleLabels).toBe(true);
    expect(f!.selSpherePoles).toBe(true);
    expect(f!.handholds).toBe(true);
    expect(f!.overlays).toBe(true);
    // labelsGlobal / badgesGlobal are HIDDEN-sense in store polarity: buffer col 1 = VISIBLE
    // → store field (labelsGlobalHidden / badgesHidden) is false.
    expect(f!.labelsGlobal).toBe(false);
    expect(f!.badgesGlobal).toBe(false);
  });

  it("reflects a toggle round-trip: a new snapshot with a flipped column changes state", () => {
    // Master overlays off + tori off; labels/badges become HIDDEN (col 0 → store true).
    setLatestSnapshot(makeOverlaySnapshot({
      [OVERLAY_COL_OVERLAYS_VIS]: 0,
      [OVERLAY_COL_SCENE_TORI]: 0,
      [OVERLAY_COL_LABELS_GLOBAL]: 0,
      [OVERLAY_COL_BADGES_GLOBAL]: 0,
    }));
    const f = readOverlayFlags()!;
    expect(f.overlays).toBe(false);
    expect(f.tori).toBe(false);
    expect(f.labelsGlobal).toBe(true); // hidden
    expect(f.badgesGlobal).toBe(true); // hidden
  });

  it("returns a stable object identity while flags are unchanged", () => {
    const a = readOverlayFlags();
    const b = readOverlayFlags();
    expect(a).toBe(b);
    // A new snapshot with the SAME flag bits keeps identity (no needless re-render).
    setLatestSnapshot(makeOverlaySnapshot({
      [OVERLAY_COL_SCENE_TORI]: 1,
      [OVERLAY_COL_SCENE_POLES]: 1,
      [OVERLAY_COL_NODE_POLES]: 1,
      [OVERLAY_COL_ANGLE_LABELS]: 1,
      [OVERLAY_COL_SEL_SPHERE_POLES]: 1,
      [OVERLAY_COL_HANDHOLDS]: 1,
      [OVERLAY_COL_LABELS_GLOBAL]: 1,
      [OVERLAY_COL_BADGES_GLOBAL]: 1,
      [OVERLAY_COL_OVERLAYS_VIS]: 1,
    }));
    expect(readOverlayFlags()).toBe(a);
  });
});
