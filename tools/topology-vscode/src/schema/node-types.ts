// Node-type registry. Single source of truth for ports and visual
// styling per node type. `kind` values must match SVG edge classes
// from docs/svg-style-guide.md §5.
//
// Generated kinds (Input, ReadGate, ChainInhibitor, InhibitRightGate)
// are derived from NODE_DEFS in node-defs.ts. Do not hand-edit them
// here — edit SPEC.md and re-run `npm run gen:node-defs`.
//
// Generic is a hand-maintained test-only placeholder for fixtures
// that need a node kind without a Go runtime.

import type { NodeTypeDef } from "./types-graph";
import { NODE_DEFS } from "../webview/rf/nodes/node-defs";

// Re-export RUNTIME_IMPLEMENTED_KINDS from generated source.
export { RUNTIME_IMPLEMENTED_KINDS } from "../webview/rf/nodes/node-defs";

// Lift generated NodeDef entries into NodeTypeDef shape.
// NODE_DEFS keys are camelCase (RF type names); NODE_TYPES keys are
// PascalCase (spec kind names). The mapping is: capitalize first char.
function defToTypeDef(rfKey: string): NodeTypeDef | undefined {
  const d = NODE_DEFS[rfKey];
  if (!d) return undefined;
  return {
    role: d.role ?? "generic",
    inputs: (d.inputs ?? []) as NodeTypeDef["inputs"],
    outputs: (d.outputs ?? []) as NodeTypeDef["outputs"],
    shape: (d.shape ?? "rect") as NodeTypeDef["shape"],
    fill: d.fill ?? d.bg,
    stroke: d.stroke ?? d.border,
    width: d.width ?? d.minWidth ?? 110,
    height: d.height ?? 60,
  };
}

export const NODE_TYPES: Record<string, NodeTypeDef> = {
  Input: defToTypeDef("input")!,
  ReadGate: defToTypeDef("readGate")!,
  ChainInhibitor: defToTypeDef("chainInhibitor")!,
  InhibitRightGate: defToTypeDef("inhibitRightGate")!,
  Generic: {
    role: "generic",
    inputs: [], outputs: [],
    shape: "rect", fill: "#ffffff", stroke: "#888", width: 110, height: 60,
  },
};
