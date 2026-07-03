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

// Compile-time union<->array parity (same guarantee KIND_COLORS gets from
// Record<EdgeKind, string>): `as const satisfies` rejects a typo or an extra member,
// and the MustEqual assertion below rejects a MISSING member. Adding an EdgeKind
// without listing it here (or vice-versa) then fails tsc instead of silently
// desyncing the runtime array from the type.
export const EDGE_KINDS = [
  "chain", "signal", "release", "streak",
  "pointer", "and-out", "edge-connection", "inhibit-in", "any",
] as const satisfies readonly EdgeKind[];

// True iff A and B are the same set (mutually assignable). Used as a coverage type:
// `const _ : MustEqual<Union, ArrayUnion> = true` fails to compile when they diverge.
type MustEqual<A, B> = [A] extends [B] ? ([B] extends [A] ? true : never) : never;
// eslint-disable-next-line @typescript-eslint/no-unused-vars
const _edgeKindsParity: MustEqual<EdgeKind, (typeof EDGE_KINDS)[number]> = true;

export const DEFAULT_EDGE_KIND: EdgeKind = "signal";

export type Port = {
  name: string;
  kind: EdgeKind;
  required?: boolean;
  // Ring anchor index: index into the evenly-spaced ring of port positions
  // computed by Go (N = floor(2πR / (d+p)), d=8, p=2). Absent = 0.
  anchorId?: number;
  // Per-port radius (world units): distance from the node center at which this
  // port is drawn and its edge attaches. Go-owned and authoritative when present;
  // absent falls back to nodeRadius(kind) = min(w,h)/4 (see portRadiusByName in
  // port_geometry.go).
  portR?: number;
};
export type StateValue = string | number;
export type SendRule = "consumeGated" | "fireAndForget";
export const SEND_RULES = ["consumeGated", "fireAndForget"] as const satisfies readonly SendRule[];
// eslint-disable-next-line @typescript-eslint/no-unused-vars
const _sendRulesParity: MustEqual<SendRule, (typeof SEND_RULES)[number]> = true;
