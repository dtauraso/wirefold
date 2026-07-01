// edge-geometry.ts — Go-authoritative edge segment store (Phase 3).
//
// Go owns node positions + per-edge straight-segment endpoints; it streams a
// `geometry` trace event per edge on load and again whenever a node-move re-derives
// that edge's segment (MODEL.md "Geometry and time"). pump.ts writes those endpoints
// here; SingleEdgeTube subscribes and draws the wire tube from them. TS
// computes NO geometry of its own — this store is plot-only, fed entirely by Go.
//
// Keyed by edge id (== the Go edge label). A zustand store (not the plain imperative
// map used for bead positions) so SingleEdgeTube re-renders when its edge's segment
// changes during a drag.

import { create } from "zustand";

/** One edge's Go-streamed straight-segment endpoints (source OUT pos, dest IN pos). */
interface EdgeSegment {
  start: { x: number; y: number; z: number };
  end: { x: number; y: number; z: number };
}

interface EdgeGeometryState {
  /** edgeId → Go-streamed segment endpoints. Absent until the first geometry event. */
  segments: Record<string, EdgeSegment>;
  /** Replace one edge's segment endpoints (pump, on each geometry trace event). */
  setEdgeSegment: (edgeId: string, s: EdgeSegment) => void;
  /** Drop one edge's segment (on edge delete) so a stale segment can't draw. */
  removeEdgeSegment: (edgeId: string) => void;
  /** Drop ALL segments (on run-restart) — a fresh run re-streams every edge. */
  clearAllEdgeSegments: () => void;
}

export const useEdgeGeometryStore = create<EdgeGeometryState>((set) => ({
  segments: {},
  setEdgeSegment: (edgeId, s) =>
    set((state) => ({ segments: { ...state.segments, [edgeId]: s } })),
  removeEdgeSegment: (edgeId) =>
    set((state) => {
      if (!(edgeId in state.segments)) return state;
      const next = { ...state.segments };
      delete next[edgeId];
      return { segments: next };
    }),
  clearAllEdgeSegments: () => set({ segments: {} }),
}));

/**
 * Wipe all edge segments at the run-start boundary (symmetric with clearAllPulses):
 * Go is re-spawned fresh and re-streams every edge's geometry event, so stale
 * segments for deleted edges must not persist across edit-reload cycles.
 */
export function clearAllEdgeSegments() {
  useEdgeGeometryStore.getState().clearAllEdgeSegments();
}
