// rule-eq-types.ts — shared pure types/constants for the typed "+ Add equation" form
// (split out of RuleEquationPanel.tsx so TypedNodeNodeForm/TypedPortTorusForm can share
// them without a circular import back through the parent). No React, no state authority —
// TypedFormState is the shape of RuleEquationPanel's own local useState value.

import type { PortOption } from "./equation-form";

/** Angle-chip glyphs for the packed term code (matches gesture.go's ruleTermCode: 0=θ,
 *  1=φ, 2=−θ, 3=−φ, 4=r — positive θ/φ show no sign). */
export const ANGLE_CHIPS = ["θ", "φ", "−θ", "−φ", "r"];

export function angleChip(code: number): string {
  return ANGLE_CHIPS[code] ?? "?";
}

// Blank order for node=node is center, nodeA, compA, nodeB, compB (`active` 0..4); Go's
// builder latches comp-then-node per term, so this form BUFFERS the term's node blank
// locally (pendingNodeRow/pendingNodeLabel) until the following comp blank resolves, then
// sends encodeAuthorLatch(comp,sign) followed by encodeAuthorNode(nodeRow) in that order —
// center is a lone AuthorNode with nothing buffered.
export const NN_CENTER = 0;
export const NN_NODE_A = 1;
export const NN_COMP_A = 2;
export const NN_NODE_B = 3;
export const NN_COMP_B = 4;

// port∈torus has a SINGLE blank: portName (autocomplete over the sticky Center's own
// ports). The torus is ALWAYS the port's own node — the sticky Center — so it is preset,
// display-only, never typed (see MODEL.md / gesture.go addPortTorusLock). Selecting a port
// option commits the lock immediately (one step) and closes the form.
export const PT_PORT_NAME = 0;

export interface TypedFormState {
  kind: number; // POLAR_LOCK_KIND_NODE_NODE | POLAR_LOCK_KIND_PORT_TORUS
  active: number; // blank index — semantics depend on kind (NN_* / PT_*)
  text: string;
  // node=node: the node blank of the term currently in progress, buffered until its
  // following comp blank resolves (Go latches comp-then-node, the form fills node-then-comp).
  pendingNodeRow: number;
  pendingNodeLabel: string;
  // port∈torus: the portNode blank's resolved row/label, buffered until AuthorPort is sent
  // (once the portName blank resolves) — same buffering shape as node=node's pendingNode*.
  portOptions: PortOption[];
  portHighlight: number;
}

export function beginForm(kind: number): TypedFormState {
  return {
    kind,
    active: 0,
    text: "",
    pendingNodeRow: -1,
    pendingNodeLabel: "",
    portOptions: [],
    portHighlight: 0,
  };
}

export function filteredPortOptions(f: TypedFormState): PortOption[] {
  const t = f.text.trim().toLowerCase();
  if (!t) return f.portOptions;
  return f.portOptions.filter((o) => o.name.toLowerCase().includes(t));
}
