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
  // Ring anchor index: index into the evenly-spaced ring of port positions
  // computed by Go (N = floor(2πR / (d+p)), d=8, p=2). Absent = 0.
  anchorId?: number;
};
export type StateValue = string | number;
export type SendRule = "consumeGated" | "fireAndForget";
export const SEND_RULES: readonly SendRule[] = ["consumeGated", "fireAndForget"];
