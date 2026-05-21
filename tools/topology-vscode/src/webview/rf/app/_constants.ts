import { FoldNode } from "../FoldNode";
import { NoteNode } from "../NoteNode";
import { GenericNode } from "../nodes/GenericNode";
import { NODE_DEFS } from "../nodes/node-defs";
import { SubstrateEdge } from "../edges/SubstrateEdge";

export const EDGE_TYPES = { substrate: SubstrateEdge };

export const RF_NODE_TYPES = {
  fold: FoldNode,
  note: NoteNode,
  ...Object.fromEntries(Object.keys(NODE_DEFS).map((k) => [k, GenericNode])),
};

// Alignment-guide tolerance is in flow units; 4 covers off-grid drag
// noise without firing on every near-miss.
export const ALIGN_TOL = 4;

export const FLASH_TIMEOUT_MS = 1500;

export const FIT_VIEW_DURATION_MS = 250;
export const FIT_VIEW_PADDING = 0.2;
export const FIT_VIEW_PADDING_WIDE = 0.4;
export const FIT_VIEW_MAX_ZOOM = 1.2;

// Re-export canonical list so call sites don't maintain a duplicate.
export { EDGE_KINDS as EDGE_KIND_OPTIONS } from "../../../schema";
