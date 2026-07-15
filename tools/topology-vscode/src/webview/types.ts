// Typed data payloads for nodes and edges. These mirror Spec.Node / Spec.Edge fields.
// Schema-only: no runtime state lives here.

import type { Port, SendRule, StateValue } from "../schema/types";
import type { NodeSpec } from "../schema/types-graph";
import type { WireProps } from "../schema/wire-defs";

// Per-node data payload.
export interface NodeData {
  // --- Spec.Node fields ---
  type: string;
  index?: number;
  props?: Record<string, StateValue>;
  spec?: NodeSpec;
  notes?: string;
  /** Raw Spec.Node.data — carried verbatim for round-trip. */
  nodeData?: unknown;
  inputs: Port[];
  outputs: Port[];
  /**
   * Sphere-chain layout fields carried from Spec.Node.r / Spec.Node.dir.
   * `r` is the node's sphere radius; `dir` the unit direction on its parent's
   * sphere. Used only as a pre-emit hint; authoritative WORLD centers come from
   * Go's node-geometry stream (sphere_layout.go propagation from anchor "1").
   */
  r?: number;
  dir?: [number, number, number];
  /** Spec-side Go field seeds (data.state in JSON). Distinct from viewer state below. */
  initState?: Record<string, number>;
  /**
   * Node-owned per-output-port send policy, keyed by output port name (the
   * sourceHandle, e.g. "ToNext0"). Lives at node.data.sendRules in the spec and
   * is carried verbatim through nodeData on round-trip (no editor UI yet).
   * Absent ports default to consumeGated. The send rule belongs to the SOURCE
   * NODE, not the edge.
   */
  sendRules?: Record<string, SendRule>;


  // --- Viewer-only fields (from NodeView / store) ---
  x?: number;
  y?: number;
  // 3D depth coordinate. Defaults to 0 when absent (exact 2D replica).
  z?: number;
  state?: Record<string, StateValue>;
/** Viewer-only fade mask. Faded nodes render muted; their incident edges draw no pulse. */
  faded?: boolean;

  // --- Adapter convenience fields ---
  label: string;
  fill: string;
  stroke: string;
  shape: "rect" | "pill";
  width: number;
  height: number;
}

// Per-edge data payload. sourceHandle/targetHandle are stored here for round-trip
// fidelity: the spec serializer reads handle names from EdgeData so they survive
// save→load without a separate mapping step.
export interface EdgeData extends WireProps {
  /** Raw Spec.Edge.data — carried verbatim for round-trip. */
  edgeData?: unknown;
  route?: string;
  value?: unknown;
  /** Original sourceHandle stored in data for round-trip. */
  sourceHandle?: string;
  /** Original targetHandle stored in data for round-trip. */
  targetHandle?: string;

  /** Viewer-only fade mask. Faded edges render muted and draw no pulse. */
  faded?: boolean;
}
