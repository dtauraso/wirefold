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
  /** Go-owned sphere radius used for bead orbit and port placement (nodeR in Go). */
  sphereR: number;
  /** Go-owned vertical great-circle ring normal (default: {x:0,y:0,z:1}). */
  vrx: number; vry: number; vrz: number;
  /** Go-owned flat (equatorial) great-circle ring normal (default: {x:0,y:1,z:0}). */
  frx: number; fry: number; frz: number;
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
    sphereR: number,
    vrx: number, vry: number, vrz: number,
    frx: number, fry: number, frz: number,
    ports: NodePortGeom[],
  ) => void;
  /** Drop one node's geometry (on node delete) so stale geometry can't be read. */
  removeNodeGeometry: (nodeId: string) => void;
  /** Drop ALL node geometry (on run-restart) — a fresh run re-streams every node. */
  clearAllNodeGeometry: () => void;
}

export const useNodeGeometryStore = create<NodeGeometryState>((set) => ({
  geoms: {},
  setNodeGeometry: (nodeId, center, radius, sphereR, vrx, vry, vrz, frx, fry, frz, ports) =>
    set((s) => ({ geoms: { ...s.geoms, [nodeId]: { center, radius, sphereR, vrx, vry, vrz, frx, fry, frz, ports } } })),
  removeNodeGeometry: (nodeId) =>
    set((s) => {
      if (!(nodeId in s.geoms)) return s;
      const next = { ...s.geoms };
      delete next[nodeId];
      return { geoms: next };
    }),
  clearAllNodeGeometry: () => set({ geoms: {} }),
}));

/** Non-React read of one node's Go-streamed geometry (for imperative call sites). */
export function getNodeGeometry(nodeId: string): NodeGeom | undefined {
  return useNodeGeometryStore.getState().geoms[nodeId];
}

/**
 * Wipe all node geometry at the run-start boundary (symmetric with clearAllPulses):
 * Go is re-spawned fresh and re-streams every node's node-geometry event, so stale
 * entries for deleted nodes must not persist across edit-reload cycles.
 */
export function clearAllNodeGeometry() {
  useNodeGeometryStore.getState().clearAllNodeGeometry();
}
