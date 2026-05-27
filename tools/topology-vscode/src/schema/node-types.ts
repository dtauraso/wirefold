// Node-type registry. Single source of truth for ports and visual
// styling per node type. `kind` values must match SVG edge classes
// from docs/svg-style-guide.md §5.
//
// Both the PascalCase keys and values of NODE_TYPES are derived from
// NODE_DEFS in node-defs.ts. Do not hand-edit kinds here — edit
// SPEC.md and re-run `npm run gen:node-defs`.
//
// Generic is a hand-maintained test-only placeholder for fixtures
// that need a node kind without a Go runtime.

import type { NodeTypeDef } from "./types-graph";
import { NODE_DEFS } from "./node-defs";

// Re-export RUNTIME_IMPLEMENTED_KINDS from generated source.
export { RUNTIME_IMPLEMENTED_KINDS } from "./node-defs";

// Lift generated NodeDef entries into NodeTypeDef shape.
// NODE_DEFS keys are PascalCase (spec kind names); NODE_TYPES keys match.
function defToTypeDef(key: string): NodeTypeDef | undefined {
  const d = NODE_DEFS[key];
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
  ...Object.fromEntries(
    Object.keys(NODE_DEFS).map((k) => [k, defToTypeDef(k)!]),
  ),
  Generic: { role: "generic", inputs: [], outputs: [], shape: "rect", fill: "#ffffff", stroke: "#888", width: 110, height: 60 },
};
