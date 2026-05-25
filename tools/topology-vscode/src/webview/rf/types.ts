// Typed data payloads for React Flow nodes and edges produced by spec-to-flow.
// These mirror Spec.Node / Spec.Edge fields plus viewer-only and adapter fields.
// Consumers still read from Zustand; this file is schema-only.

import type { Port, StateValue } from "../../schema/types";
import type { NodeSpec } from "../../schema/types-graph";
import type { WireProps } from "../../schema/wire-defs";
import { ANIMATION_FIELDS } from "./animation-fields";

// Per-node data carried in RF Node<NodeData>.data.
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
  /** Spec-side Go field seeds (data.state in JSON). Distinct from viewer state below. */
  initState?: Record<string, number>;
  edgeSeeds?: Record<string, number>;

  // --- Viewer-only fields (from NodeView / store) ---
  x?: number;
  y?: number;
  sublabel?: string;
  foldId?: string;
  dimmed?: boolean;
  state?: Record<string, StateValue>;
  /** Set when a required inbound edge is missing; causes the node to render with a red border. */
  validationError?: string;

  // --- Runtime trace fields (Phase 4) ---
  /** Last fire event step for this node (used for visual highlight). */
  lastFire?: typeof ANIMATION_FIELDS["lastFire"]["type"];
  // --- Adapter convenience fields ---
  label: string;
  fill: string;
  stroke: string;
  shape: "rect" | "pill";
  width: number;
  height: number;
}

// Per-edge data carried in RF Edge<EdgeData>.data.
// RF-native fields (id, source, sourceHandle, target, targetHandle) live on
// the RF edge itself and are NOT duplicated here — except sourceHandle /
// targetHandle are also stored in data for round-trip when endpoints are
// rerouted through a fold placeholder.
export interface EdgeData extends WireProps {
  /** Raw Spec.Edge.data — carried verbatim for round-trip. */
  edgeData?: unknown;
  route?: string;
  value?: unknown;
  /** Original sourceHandle before any fold rerouting. */
  sourceHandle?: string;
  /** Original targetHandle before any fold rerouting. */
  targetHandle?: string;

  // --- Runtime trace fields (Phase 4) ---
  /** Set by pump on a "send" event: the value in flight on this edge. */
  pulse?: typeof ANIMATION_FIELDS["pulse"]["type"];
}

// Runtime trace fields added to NodeData (Phase 4).
// Declared as an augmentation here to keep the main NodeData block readable.
