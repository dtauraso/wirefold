// Imperative interior-bead store for node 1's 2x2 buffer. Mirrors pulse-state.ts:
// Go's node-bead snapshot stream is the SOLE source of slot state — TS plots, it
// computes no geometry. pump.ts calls setInteriorBead on every node-bead event (a
// 4-slot snapshot per array change); InteriorBeads (scene-content.tsx) reads
// getInteriorBeadMap() imperatively each frame and places each PRESENT slot's mesh
// at its Go-supplied position, hiding empty slots.
//
// Plain non-React Map: there is no React subscriber (the renderer polls in
// useFrame), so updates mutate the map without triggering a commit.
//
// Key: `${node}:${row}:${col}`. Value: { value, present, pos }. On present=false the
// slot is marked empty (the bead is hidden) — absence streamed explicitly so a
// popped bead disappears.

interface InteriorBeadData {
  value: number;
  present: boolean;
  /** Go-computed slot NODE-LOCAL offset (relative to node center). TS never computes
   *  this; it places the bead as a child of the node group, so world = center + offset. */
  pos: { x: number; y: number; z: number };
}

export type InteriorBeadMap = ReadonlyMap<string, InteriorBeadData>;

export function interiorBeadKey(node: string, row: number, col: number): string {
  return `${node}:${row}:${col}`;
}

let _current: Map<string, InteriorBeadData> = new Map();

/** Record one slot's state from a node-bead trace event. Mutates in place (no React
 *  commit). present=false marks the slot empty so the renderer hides it. */
export function setInteriorBead(
  node: string,
  row: number,
  col: number,
  present: boolean,
  value: number,
  pos: { x: number; y: number; z: number },
) {
  _current.set(interiorBeadKey(node, row, col), { value, present, pos });
}

/** Wipe every slot. Called at run-start (store.load) so a fresh run's process
 *  does not inherit a stale slot left in the store from a prior run that was
 *  stopped. Mirrors clearAllPulses: swaps _current for a fresh Map — InteriorBeads
 *  polls getInteriorBeadMap in useFrame, so the next frame draws no stale slots. */
export function clearInteriorBeads() {
  _current = new Map();
}

export function getInteriorBeadMap(): InteriorBeadMap {
  return _current;
}
