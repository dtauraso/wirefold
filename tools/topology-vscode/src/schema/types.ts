// Atomic types + handler formalism. Graph types (Node/Edge/Spec) live
// in types-graph.ts; the runtime registry and parsers live alongside.

export type EdgeKind =
  | "chain"
  | "signal"
  | "release"
  | "streak"
  | "pointer"
  | "and-out"
  | "edge-connection"
  | "inhibit-in"
  | "any";

export const EDGE_KINDS: readonly EdgeKind[] = [
  "chain", "signal", "release", "streak",
  "pointer", "and-out", "edge-connection", "inhibit-in", "any",
];

export const DEFAULT_EDGE_KIND: EdgeKind = "signal";

export type Port = {
  name: string;
  kind: EdgeKind;
  required?: boolean;
  // Visual placement. Independent of input/output: inputs default to
  // "left" and outputs to "right", but any port may be placed on any
  // side. Layout-only — has no Go-model effect.
  side?: "left" | "right" | "top" | "bottom";
  // Snap slot along the side: 0=25%, 1=50%, 2=75%. Absent = auto-space.
  slot?: 0 | 1 | 2;
  // 3D port-anchor: offset from node center in 3D space. When absent,
  // derive from side+slot so existing 2D topology is unaffected.
  // Additive/new — does not replace side/slot for the RF 2D editor.
  anchor?: { x: number; y: number; z: number };
};
export type StateValue = string | number;
export type ArrowStyle = "filled" | "open";
export type SendRule = "consumeGated" | "fireAndForget";
export const SEND_RULES: readonly SendRule[] = ["consumeGated", "fireAndForget"];
