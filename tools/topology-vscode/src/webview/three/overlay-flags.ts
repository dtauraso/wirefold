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
import { getLatestSnapshot, subscribeSnapshot } from "../snapshot-buffer";
import { decodeSnapshot } from "./buffer-decode";
import {
  readOverlaySceneTori,
  readOverlayScenePoles,
  readOverlayNodePoles,
  readOverlaySelSpherePoles,
  readOverlayHandholds,
  readOverlayLabelsGlobal,
  readOverlayBadgesGlobal,
  readOverlayOverlaysVis,
  readOverlayDoubleLinks,
} from "../../schema/buffer-layout";

// Keyed by OverlayFlag. Polarity is MIXED — a historical wart worth stating plainly, since
// the ViewerState key names it mirrored are gone (that state island was deleted once Go
// owned scene persistence):
//   • most flags are visible-sense (true = shown) — <x>Visible
//   • labelsGlobal / badgesGlobal are HIDDEN-sense (true = hidden) —
//     labelsGlobalHidden / badgesHidden. The buffer stores visible-sense, so we
//     invert those two here.
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
  const snap = getLatestSnapshot();
  if (!snap) return cachedVals;
  const decoded = decodeSnapshot(snap);
  if (!decoded) return cachedVals;
  const v = decoded.overlayView;
  const bits =
    (readOverlaySceneTori(v) ? 1 << 0 : 0) |
    (readOverlayScenePoles(v) ? 1 << 1 : 0) |
    (readOverlayNodePoles(v) ? 1 << 2 : 0) |
    (readOverlaySelSpherePoles(v) ? 1 << 3 : 0) |
    (readOverlayHandholds(v) ? 1 << 4 : 0) |
    (readOverlayLabelsGlobal(v) ? 1 << 5 : 0) |
    (readOverlayBadgesGlobal(v) ? 1 << 6 : 0) |
    (readOverlayOverlaysVis(v) ? 1 << 7 : 0) |
    (readOverlayDoubleLinks(v) ? 1 << 8 : 0);
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
    badgesGlobal: !(bits & (1 << 6)),
    overlays: !!(bits & (1 << 7)),
    doubleLinks: !!(bits & (1 << 8)),
  };
  return cachedVals;
}

/** React hook: re-renders the caller when any overlay flag flips (Go-owned). Returns
 *  null until the first snapshot lands. */
export function useOverlayFlags(): OverlayFlagVals | null {
  return useSyncExternalStore(subscribeSnapshot, readOverlayFlags, readOverlayFlags);
}
