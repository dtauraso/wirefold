// overlay-flags.ts — a row-keyed READ resource over the buffer's Overlay columns.
//
// The overlay on/off state is Go-owned: Go flips it on the
// `edit op=update kind=overlays` command and streams the updated flags into the
// buffer's Overlay block (Buffer/snapshot.go). This module REFLECTS those Go-owned
// columns for widgets that must re-render when a flag flips (the overlay toggle
// control, NavGuides gating). It is NOT a domain store — it authors nothing; it only
// decodes the latest snapshot's Overlay row and subscribes to snapshot arrivals so a
// toggle round-trips to the displayed state.

import { useSyncExternalStore } from "react";
import type { OverlayFlag } from "../../messages";
import { getNodeFrameOrFallback, subscribeNodeStreamBlocks } from "./node-stream-blocks";
import { getViewBlocks, subscribeViewBlocks } from "./view-blocks";
import {
  readOverlaySceneTori,
  readOverlayScenePoles,
  readOverlayNodePoles,
  readOverlaySelSpherePoles,
  readOverlayHandholds,
  readOverlayLabelsGlobal,
  readOverlayOverlaysVis,
  readOverlayDoubleLinks,
  readOverlayAbcDragCount,
  readNodeGotDragMsg,
  readNodeDragDeltaA,
  readNodeDragDeltaB,
  readNodeDragDeltaC,
} from "../../schema/buffer-layout";
import { nodeLabel } from "./buffer-decode";

// Keyed by OverlayFlag. Polarity is MIXED — a historical wart worth stating plainly, since
// the ViewerState key names it mirrored are gone (that state island was deleted once Go
// owned scene persistence):
//   • most flags are visible-sense (true = shown) — <x>Visible
//   • labelsGlobal is HIDDEN-sense (true = hidden) — labelsGlobalHidden. The buffer
//     stores visible-sense, so we invert that one here.
export type OverlayFlagVals = Record<OverlayFlag, boolean>;

// Cache so getSnapshot returns a STABLE object identity while the flags are unchanged
// (useSyncExternalStore compares by identity; a fresh object every 60fps snapshot would
// re-render every frame). We recompute the bit-set each call — cheap — and only mint a
// new OverlayFlagVals when a flag actually flips.
let cachedBits = -1;
let cachedVals: OverlayFlagVals | null = null;

/** Decode the latest snapshot's Overlay row into store-polarity booleans, or null if no
 *  snapshot / decode failure. Stable identity while unchanged. */
export function readOverlayFlags(): OverlayFlagVals | null {
  const blocks = getViewBlocks();
  if (!blocks) return cachedVals;
  const v = blocks.overlayView;
  const bits =
    (readOverlaySceneTori(v) ? 1 << 0 : 0) |
    (readOverlayScenePoles(v) ? 1 << 1 : 0) |
    (readOverlayNodePoles(v) ? 1 << 2 : 0) |
    (readOverlaySelSpherePoles(v) ? 1 << 3 : 0) |
    (readOverlayHandholds(v) ? 1 << 4 : 0) |
    (readOverlayLabelsGlobal(v) ? 1 << 5 : 0) |
    (readOverlayOverlaysVis(v) ? 1 << 6 : 0) |
    (readOverlayDoubleLinks(v) ? 1 << 7 : 0);
  if (bits === cachedBits && cachedVals) return cachedVals;
  cachedBits = bits;
  cachedVals = {
    tori: !!(bits & (1 << 0)),
    scenePoles: !!(bits & (1 << 1)),
    nodePoles: !!(bits & (1 << 2)),
    selSpherePoles: !!(bits & (1 << 3)),
    handholds: !!(bits & (1 << 4)),
    // hidden-sense: buffer stores VISIBLE, store field is *Hidden → invert.
    labelsGlobal: !(bits & (1 << 5)),
    overlays: !!(bits & (1 << 6)),
    doubleLinks: !!(bits & (1 << 7)),
  };
  return cachedVals;
}

/** React hook: re-renders the caller when any overlay flag flips (Go-owned). Returns
 *  null until the first snapshot lands. */
export function useOverlayFlags(): OverlayFlagVals | null {
  return useSyncExternalStore(subscribeViewBlocks, readOverlayFlags, readOverlayFlags);
}

/** Decode the latest snapshot's running time-node abc-drag event count (Overlay block
 *  AbcDragCount column). Read-only affirmation counter — never authored by TS, only
 *  reflects the buffer. Returns 0 if no snapshot / decode failure yet. */
export function readAbcDragCount(): number {
  const blocks = getViewBlocks();
  if (!blocks) return 0;
  return readOverlayAbcDragCount(blocks.overlayView);
}

/** React hook: re-renders the caller as time.abc-drag events accumulate (Go-owned
 *  counter; affirms the drag-log is happening live). */
export function useAbcDragCount(): number {
  return useSyncExternalStore(subscribeViewBlocks, readAbcDragCount, readAbcDragCount);
}

/** One current-drag recipient: its display name plus the DRAGGED node's own
 *  quantized-triple delta (dA,dB,dC) that rode the message this recipient received
 *  (Node block DragDeltaA/B/C columns). */
export interface AbcDragRow {
  name: string;
  dA: number;
  dB: number;
  dC: number;
}

let cachedRowsKey = "\0";
let cachedRows: AbcDragRow[] = [];

/** Decode the current drag's recipient ROWS (name + received delta triple) from the
 *  Node block's per-row GotDragMsg flag + DragDeltaA/B/C columns. Go-owned and
 *  drag-scoped (cleared on KindAbcDragReset at drag start, which emits the cleared
 *  state — so an empty result is meaningful). Stable identity while unchanged —
 *  including a (0,0,0) delta row, which is real information ("got the message, didn't
 *  move"), not absence. */
export function readAbcDragRows(): AbcDragRow[] {
  const decoded = getNodeFrameOrFallback();
  if (!decoded) return cachedRows;
  const rows: AbcDragRow[] = [];
  for (let row = 0; row < decoded.nodeCount; row++) {
    if (!readNodeGotDragMsg(decoded.nodeView, row)) continue;
    rows.push({
      name: nodeLabel(decoded, row),
      dA: readNodeDragDeltaA(decoded.nodeView, row),
      dB: readNodeDragDeltaB(decoded.nodeView, row),
      dC: readNodeDragDeltaC(decoded.nodeView, row),
    });
  }
  const key = rows.map((r) => `${r.name}\0${r.dA},${r.dB},${r.dC}`).join("\0");
  if (key === cachedRowsKey) return cachedRows;
  cachedRowsKey = key;
  cachedRows = rows;
  return cachedRows;
}

/** React hook: re-renders the caller when the current drag's recipient rows change —
 *  INCLUDING when cleared to empty at drag start. The GotDragMsg/DragDeltaA-C columns
 *  live in the Node block (node-owner-group frame), so this subscribes to node-frame
 *  arrivals, not scene-frame arrivals. */
export function useAbcDragRows(): AbcDragRow[] {
  return useSyncExternalStore(subscribeNodeStreamBlocks, readAbcDragRows, readAbcDragRows);
}

