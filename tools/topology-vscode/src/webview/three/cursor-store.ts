// cursor-store.ts — last known canvas cursor position (client px), for in-scene
// guides that need to follow the mouse (e.g. the polar pan disk/triangle overlay).
// Plot-only: written by the interaction-controls pointer handler, read in a useFrame.

import { create } from "zustand";

interface CursorState {
  x: number;
  y: number;
  inside: boolean;
  set: (x: number, y: number, inside: boolean) => void;
}

export const useCursorStore = create<CursorState>((set) => ({
  x: 0,
  y: 0,
  inside: false,
  set: (x, y, inside) => set({ x, y, inside }),
}));
