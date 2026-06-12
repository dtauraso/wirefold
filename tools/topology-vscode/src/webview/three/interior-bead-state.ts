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

export interface InteriorBeadData {
  value: number;
  present: boolean;
  /** Go-computed slot world position. TS never computes this. */
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

export function getInteriorBeadMap(): InteriorBeadMap {
  return _current;
}
