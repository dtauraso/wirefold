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
import { NODE_DEFS, type NodeDef } from "./node-defs";
import { NODE_DIM_FALLBACK } from "./node-dims";

// --- Adapter coverage guard (compile-time) ---------------------------------
// NodeDef (generated) and NodeTypeDef (hand-authored) are parallel type defs
// bridged by defToTypeDef below. If the generator adds a field to NodeDef that
// this adapter neither consumes nor explicitly ignores, the field is silently
// dropped. The assertion below fails `tsc` in that case: every NodeDef key must
// be classified as either consumed by defToTypeDef or intentionally not lifted.
//
// When tsc points here, add the new field to one of the two unions — and, if it
// belongs in NodeTypeDef, also thread it through defToTypeDef.
type ConsumedNodeDefKeys =
  | "role"
  | "shape"
  | "fill"
  | "stroke"
  | "width"
  | "height"
  | "inputs"
  | "outputs";
// Raw style/source fields used by GraphNode (which reads NODE_DEFS directly) or
// folded into a NodeTypeDef field as a fallback — intentionally not lifted 1:1.
type IgnoredNodeDefKeys = "bg" | "border" | "text" | "minWidth";
type UnmappedNodeDefKeys = Exclude<keyof NodeDef, ConsumedNodeDefKeys | IgnoredNodeDefKeys>;
type AssertNodeDefCoverage = [UnmappedNodeDefKeys] extends [never] ? true : never;
const _nodeDefCoverage: AssertNodeDefCoverage = true;
void _nodeDefCoverage;

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
    width: d.width ?? d.minWidth ?? NODE_DIM_FALLBACK.width,
    height: d.height ?? NODE_DIM_FALLBACK.height,
  };
}

export const NODE_TYPES: Record<string, NodeTypeDef> = {
  ...Object.fromEntries(
    Object.keys(NODE_DEFS).flatMap((k) => {
      const def = defToTypeDef(k);
      return def ? [[k, def]] : [];
    }),
  ),
  Generic: { role: "generic", inputs: [], outputs: [], shape: "rect", fill: "#ffffff", stroke: "#888", width: NODE_DIM_FALLBACK.width, height: NODE_DIM_FALLBACK.height },
};
