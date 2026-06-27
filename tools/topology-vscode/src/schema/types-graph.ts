// Graph + spec types: Node, Edge, NodeSpec, NodeTypeDef, Note, and the Spec wrapper.

import type {
  EdgeKind,
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
  // (e.g. WindowAndInhibitRightGate, an AND over N input slots).
  inputs?: Port[];
  outputs?: Port[];
  // Struct field values injected before the first tick (wire:"data.state").
  state?: Record<string, number>;
  // Sphere-chain layout (Go's sphere_layout.go). `r` is the node's sphere
  // radius; `dir` is the unit direction on its parent's sphere. Go computes
  // authoritative WORLD centers by propagation from anchor node "1" at origin
  // (child_center = parent_center + r_parent * dir_child) and streams them via
  // node-geometry. These fields only seed the pre-emit fallback; positions are
  // Go-authoritative once the first node-geometry emit arrives.
  r?: number;
  dir?: [number, number, number];

};

export type Edge = WireProps & {
  id: string;
  source: string;
  sourceHandle: string;
  target: string;
  targetHandle: string;
  data?: unknown;
};

export type Note = { x: number; y: number; width?: number; height?: number; text: string };

export type Spec = {
  nodes: Node[];
  edges: Edge[];
  notes?: Note[];
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
