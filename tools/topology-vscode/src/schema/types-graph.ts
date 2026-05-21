// Graph + spec types: Node, Edge, NodeSpec, SeedEvent, NodeTypeDef,
// LegendRow, Note, and the Spec wrapper.

import type {
  Port,
  StateValue,
} from "./types";
import type { WireProps } from "./wire-defs";

// Inline AI-authored prose describing a node's logic. Math symbols
// (≤ ≠ × → …) live inside `text` segments as Unicode. `outputRef`
// segments name an outgoing edge id; the renderer resolves them to
// the live edge color so renaming an edge recolors the prose
// automatically. Humans never type this directly — `notes` is the
// human-authored field.
export type SpecSegment = { text: string } | { outputRef: string };
export type NodeSpec = { lang: string; segments: SpecSegment[] };

export type Node = {
  id: string;
  type: string;
  index?: number;
  // Per-instance config consumed by simulator handlers (e.g. delay,
  // inputCount). Defaults come from NODE_TYPES[type].defaultProps;
  // spec only stores overrides.
  props?: Record<string, StateValue>;
  spec?: NodeSpec;
  notes?: string;
  data?: unknown;
  // Per-instance port override. When present, supersedes
  // NODE_TYPES[type].inputs/outputs. Used for variable-arity kinds
  // (e.g. ReadGate, an AND over N input slots).
  inputs?: Port[];
  outputs?: Port[];
  // Per-instance initial slot values loaded before the first tick.
  // Keys must match declared input port names.
  initialSlots?: Record<string, StateValue>;
};

export type Edge = WireProps & {
  id: string;
  source: string;
  sourceHandle: string;
  target: string;
  targetHandle: string;
  data?: unknown;
};

export type LegendRow = { kind: EdgeKind; name: string; desc: string };
export type Note = { x: number; y: number; width?: number; height?: number; text: string };

export type SeedEvent = {
  nodeId: string;
  outPort: string;
  value: StateValue;
  // Optional ignition tick. Defaults to 0.
  atTick?: number;
};

export type Spec = {
  nodes: Node[];
  edges: Edge[];
  timing?: {
    duration?: string;
    // Initial events that ignite the simulation. Phase 5.5 replaced
    // the SVG-era `steps[]` master script with this. Legacy `steps`
    // fields in older topology.json files are silently dropped at
    // parse time.
    seed?: SeedEvent[];
  };
  // Cycle counter source. If set, increments each time the named
  // node fires. If unset, falls back to (ii-a) quiescent-input.
  cycleAnchor?: string;
  legend?: LegendRow[];
  notes?: Note[];
  // Substrate selector. Default (undefined) = callback substrate.
  // "ticked" opts into the Phase 1 ticked substrate (Shape A only for now).
  runtime?: "ticked";
};

export type NodeTypeDef = {
  role: string;
  inputs: Port[];
  outputs: Port[];
  shape: "rect" | "pill";
  fill: string;
  stroke: string;
  width: number;
  height: number;
  // Defaults for spec.nodes[i].props.
  defaultProps?: Record<string, StateValue>;
};
