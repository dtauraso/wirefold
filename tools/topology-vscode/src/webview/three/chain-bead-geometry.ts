// chain-bead-geometry.ts — Go-authoritative chain-of-beads wire geometry store.
//
// In the chain-of-beads wire model, Go runs a chain of bead-item goroutines per
// edge; each item streams its world position via a `chain-bead` trace event. pump.ts
// writes those positions here; the renderer (next step) plots one sphere per bead.
// TS computes NO geometry — this store is plot-only, fed entirely by Go.
//
// Keyed edge id → bead id → world position. A zustand store so subscribers
// re-render as beads relax / are born / retired.

import { create } from "zustand";

export interface BeadPos {
  x: number;
  y: number;
  z: number;
}

interface ChainBeadState {
  /** edge id → (bead id → Go-streamed world position). */
  beads: Record<string, Record<number, BeadPos>>;
  /** Write one bead's position (pump, on each chain-bead trace event). */
  setChainBead: (edge: string, bead: number, pos: BeadPos) => void;
  /** Drop all beads for one edge (on edge delete) so stale geometry can't be read. */
  removeChain: (edge: string) => void;
}

export const useChainBeadStore = create<ChainBeadState>((set) => ({
  beads: {},
  setChainBead: (edge, bead, pos) =>
    set((s) => ({
      beads: {
        ...s.beads,
        [edge]: { ...(s.beads[edge] ?? {}), [bead]: pos },
      },
    })),
  removeChain: (edge) =>
    set((s) => {
      if (!(edge in s.beads)) return s;
      const next = { ...s.beads };
      delete next[edge];
      return { beads: next };
    }),
}));
