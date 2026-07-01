// Imperative node-status store. Mirrors interior-bead-state.ts: Go's `node-status`
// trace stream is the SOLE source of a node's processing-status render state — TS
// plots, it computes nothing. Go emits `node-status` when a DIFFERENT-color bead
// arrives on a node's input port during its processing window (torusRed=true, with
// the ignored bead's value + a world position just outside the node) and again when
// processing finishes (torusRed=false, revert). pump.ts calls setNodeStatus on every
// node-status event; GraphNode paints its border ring red while torusRed, and the
// MissedBeadMarkers renderer places a bead marker at the Go-supplied world position.
//
// Plain non-React Map: the renderers poll in useFrame, so updates mutate the map
// without triggering a React commit. Reverting is driven purely by the next
// node-status event Go sends (torusRed=false) — no TS-side timer or logic.
//
// Key: node id. Value: { torusRed, missedValue, pos }.

export interface NodeStatusData {
  /** true while Go reports a firing error (missed different-color bead). */
  torusRed: boolean;
  /** value of the ignored bead — colors the missed-bead marker like any bead. */
  missedValue: number;
  /** Go-supplied WORLD position just outside the node for the missed-bead marker. */
  pos: { x: number; y: number; z: number };
}

export type NodeStatusMap = ReadonlyMap<string, NodeStatusData>;

let _current: Map<string, NodeStatusData> = new Map();

/** Record one node's status from a node-status trace event. Mutates in place (no
 *  React commit). torusRed=false reverts the node to its normal color. */
export function setNodeStatus(
  node: string,
  torusRed: boolean,
  missedValue: number,
  pos: { x: number; y: number; z: number },
) {
  _current.set(node, { torusRed, missedValue, pos });
}

export function getNodeStatusMap(): NodeStatusMap {
  return _current;
}
