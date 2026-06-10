// edge-geometry.ts — Go-authoritative edge curve store (Phase 3).
//
// Go owns node positions + per-edge quadratic-bezier control points; it streams a
// `geometry` trace event per edge on load and again whenever a node-move re-derives
// that edge's curve (MODEL.md "Geometry and time"). pump.ts writes those control
// points here; SingleEdgeTube subscribes and draws the wire tube from them. TS
// computes NO geometry of its own — this store is plot-only, fed entirely by Go.
//
// Keyed by edge id (== the Go edge label). A zustand store (not the plain imperative
// map used for bead positions) so SingleEdgeTube re-renders when its edge's curve
// changes during a drag.

import { create } from "zustand";

/** One edge's Go-streamed control points (source OUT pos, bulge, dest IN pos). */
export interface EdgeCurvePoints {
  p0: { x: number; y: number; z: number };
  p1: { x: number; y: number; z: number };
  p2: { x: number; y: number; z: number };
}

interface EdgeGeometryState {
  /** edgeId → Go-streamed control points. Absent until the first geometry event. */
  curves: Record<string, EdgeCurvePoints>;
  /** Replace one edge's control points (pump, on each geometry trace event). */
  setEdgeCurve: (edgeId: string, c: EdgeCurvePoints) => void;
  /** Drop one edge's control points (on edge delete) so a stale curve can't draw. */
  removeEdgeCurve: (edgeId: string) => void;
}

export const useEdgeGeometryStore = create<EdgeGeometryState>((set) => ({
  curves: {},
  setEdgeCurve: (edgeId, c) =>
    set((s) => ({ curves: { ...s.curves, [edgeId]: c } })),
  removeEdgeCurve: (edgeId) =>
    set((s) => {
      if (!(edgeId in s.curves)) return s;
      const next = { ...s.curves };
      delete next[edgeId];
      return { curves: next };
    }),
}));

/** Non-React read of one edge's Go-streamed curve (for imperative call sites). */
export function getEdgeCurve(edgeId: string): EdgeCurvePoints | undefined {
  return useEdgeGeometryStore.getState().curves[edgeId];
}
