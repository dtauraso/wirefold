// pan-polar-store.ts — transient state for the polar pan overlay (PanPolarOverlay).
// Plot-only: written by onWheelNative during a scroll-pan burst, read in the overlay.
//   cx, cy — anchor (cursor) in canvas-relative px, frozen at burst start (r = 0).
//   dx, dy — accumulated wheel delta since burst start; the 3rd line is (r, angle) of this.
// The cursor does not move during a wheel burst, so the center stays at the anchor and the
// 3rd line grows out of it as deltas accumulate.

import { create } from "zustand";

interface PanPolarState {
  active: boolean;
  cx: number;
  cy: number;
  dx: number;
  dy: number;
  start: (cx: number, cy: number) => void;
  accumulate: (ddx: number, ddy: number) => void;
  end: () => void;
}

export const usePanPolarStore = create<PanPolarState>((set) => ({
  active: false,
  cx: 0,
  cy: 0,
  dx: 0,
  dy: 0,
  start: (cx, cy) => set({ active: true, cx, cy, dx: 0, dy: 0 }),
  accumulate: (ddx, ddy) => set((s) => ({ dx: s.dx + ddx, dy: s.dy + ddy })),
  end: () => set({ active: false }),
}));
