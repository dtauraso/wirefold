// Single registry for spec-kind ↔ RF-type mapping.
// Adding a new substrate kind requires editing only this file (plus the Go
// node package — a separate concern). NODE_DEFS drives both the RF node-type
// map and the schema adapter.

import { FoldNode } from "../FoldNode";
import { NoteNode } from "../NoteNode";
import { GenericNode } from "./GenericNode";
import { NODE_DEFS } from "./node-defs";

export { NODE_DEFS } from "./node-defs";

/** Converts a spec kind (PascalCase) to the RF node type name (camelCase). */
export function specKindToRfType(kind: string): string {
  return kind.charAt(0).toLowerCase() + kind.slice(1);
}

// RF_NODE_TYPES is derived from NODE_DEFS — no manual sync needed.
// Non-generic kinds (fold, note) are listed explicitly before the spread so
// they take precedence if a spec kind ever collides with those names.
export const RF_NODE_TYPES = {
  fold: FoldNode,
  note: NoteNode,
  ...Object.fromEntries(Object.keys(NODE_DEFS).map((k) => [k, GenericNode])),
};
