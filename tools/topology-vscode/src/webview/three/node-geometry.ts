// node-geometry.ts — Go-authoritative node + port geometry store (item 1).
//
// Each node's goroutine emits a `node-geometry` trace event: the node center and
// per-port world positions/dirs (in the Three y-up frame; Go's nodeWorldPos /
// portDir mirror geometry-helpers.ts line-for-line). pump.ts writes those values
// here; the geometry helpers read from here instead of recomputing from
// node.position/width/height. TS computes NO geometry of its own — this store is
// plot-only, fed entirely by Go. A pre-emit fallback to local compute lives in the
// helpers so startup never breaks.
//
// Keyed by node id (== the Go node name). A zustand store so subscribers re-render
// when a node's geometry changes (e.g. on move re-derive).

import { create } from "zustand";

export interface NodePortGeom {
  name: string;
  isInput: boolean;
  pos: { x: number; y: number; z: number };
  dir: { x: number; y: number; z: number };
}

export interface NodeGeom {
  center: { x: number; y: number; z: number };
  /** Go-owned node body/ring sphere radius (min(w,h)/divisor). */
  radius: number;
  ports: NodePortGeom[];
}

interface NodeGeometryState {
  /** nodeId → Go-streamed geometry. Absent until the first node-geometry event. */
  geoms: Record<string, NodeGeom>;
  /** Replace one node's geometry (pump, on each node-geometry trace event). */
  setNodeGeometry: (
    nodeId: string,
    center: NodeGeom["center"],
    radius: number,
    ports: NodePortGeom[],
  ) => void;
  /** Drop one node's geometry (on node delete) so stale geometry can't be read. */
  removeNodeGeometry: (nodeId: string) => void;
}

export const useNodeGeometryStore = create<NodeGeometryState>((set) => ({
  geoms: {},
  setNodeGeometry: (nodeId, center, radius, ports) =>
    set((s) => ({ geoms: { ...s.geoms, [nodeId]: { center, radius, ports } } })),
  removeNodeGeometry: (nodeId) =>
    set((s) => {
      if (!(nodeId in s.geoms)) return s;
      const next = { ...s.geoms };
      delete next[nodeId];
      return { geoms: next };
    }),
}));

/** Non-React read of one node's Go-streamed geometry (for imperative call sites). */
export function getNodeGeometry(nodeId: string): NodeGeom | undefined {
  return useNodeGeometryStore.getState().geoms[nodeId];
}
