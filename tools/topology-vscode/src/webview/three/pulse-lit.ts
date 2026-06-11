// pulse-lit.ts — Go-authoritative per-bead pulse-highlight store (chain model).
//
// As the value-bead pulse hops item-to-item along an edge's chain, Go emits a
// `pulse-lit` trace event each time a bead lights or unlights. pump.ts writes the
// state here; the renderer (next step) highlights the lit bead. TS computes no
// timing — Go owns the one clock and paces the hop; this store is plot-only.
//
// Keyed edge id → bead id → { value, lit }.

import { create } from "zustand";

export interface PulseLitState1 {
  value: number;
  lit: boolean;
}

interface PulseLitState {
  /** edge id → (bead id → highlight state). */
  lit: Record<string, Record<number, PulseLitState1>>;
  /** Write one bead's pulse highlight (pump, on each pulse-lit trace event). */
  setPulseLit: (edge: string, bead: number, value: number, lit: boolean) => void;
  /** Drop all pulse state for one edge (on edge delete). */
  removeChainPulse: (edge: string) => void;
}

export const usePulseLitStore = create<PulseLitState>((set) => ({
  lit: {},
  setPulseLit: (edge, bead, value, lit) =>
    set((s) => ({
      lit: {
        ...s.lit,
        [edge]: { ...(s.lit[edge] ?? {}), [bead]: { value, lit } },
      },
    })),
  removeChainPulse: (edge) =>
    set((s) => {
      if (!(edge in s.lit)) return s;
      const next = { ...s.lit };
      delete next[edge];
      return { lit: next };
    }),
}));
