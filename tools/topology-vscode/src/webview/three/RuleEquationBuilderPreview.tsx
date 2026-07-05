// RuleEquationBuilderPreview.tsx — the in-progress click-builder preview (renders the
// selSpherePoles authoring session live from Go-streamed RuleBuilderState), plus renderTerm,
// the single-term renderer shared with CommittedEquationsList. Split out of
// RuleEquationPanel.tsx: pure render functions of already-decoded data, no local state.

import { postGoRecord } from "../vscode-api";
import { encodeClearRule } from "../../schema/input-layout";
import type { RuleBuilderTerm, RuleBuilderState } from "./rule-builder";
import { angleChip } from "./rule-eq-types";

/** Renders one term slot: a completed term (filled), a pending half-term (node slot
 *  empty, angle chip highlighted — "show the handhold being selected"), or nothing. */
export function renderTerm(term: RuleBuilderTerm | null, pendingCode: number | null) {
  if (term != null) {
    return (
      <span className="rule-eq-term">
        (<span className="rule-eq-node">{term.label}</span>,
        <span className="rule-eq-angle">{angleChip(term.code)}</span>)
      </span>
    );
  }
  if (pendingCode != null) {
    return (
      <span className="rule-eq-term rule-eq-term--pending">
        (<span className="rule-eq-node rule-eq-node--awaiting">_</span>,
        <span className="rule-eq-angle rule-eq-angle--pending">{angleChip(pendingCode)}</span>)
      </span>
    );
  }
  return null;
}

/** Renders the in-progress equation-being-authored section (the selSpherePoles session).
 *  A `port ∈ torus` authoring capture (rb.pendingPort/rb.pendingTorus) is INDEPENDENT of
 *  the node/node pending term above — if either side is picked, render the port∈torus
 *  in-progress form instead of the node/node builder preview. */
export function renderBuilder(rb: RuleBuilderState) {
  if (rb.pendingPort != null || rb.pendingTorus != null) {
    return renderPortTorusBuilder(rb);
  }
  // Left term = the first completed term, or (when none completed yet) the pending
  // half-term itself — "show the handhold being selected" before any node is picked.
  const leftTerm = rb.terms[0] ?? null;
  const rightTerm = rb.terms[1] ?? null;
  // The pending half-term slots in wherever a term is still missing: after the left term
  // (awaiting the second handhold) or as the left term itself (nothing completed yet).
  const pendingSlot: "left" | "right" | null =
    rb.pending == null ? null : leftTerm == null ? "left" : rightTerm == null ? "right" : null;

  // The clear button is armed only when there is an in-progress equation to discard (a
  // pending half-term or at least one completed term). Go owns the state; the button just
  // sends the bare clear command (fire-and-forget).
  const hasInProgress = rb.pending != null || rb.terms.length > 0;

  return (
    <>
      <div className="rule-eq-equation">
        {renderTerm(leftTerm, pendingSlot === "left" ? rb.pending!.code : null)}
        {(rightTerm != null || pendingSlot === "right") && (
          <>
            <span className="rule-eq-op"> = </span>
            {renderTerm(rightTerm, pendingSlot === "right" ? rb.pending!.code : null)}
          </>
        )}
      </div>
      <button
        className="rule-eq-clear"
        disabled={!hasInProgress}
        title="Clear the equation being built"
        onClick={() => postGoRecord(encodeClearRule())}
      >
        Clear
      </button>
    </>
  );
}

/** Renders the in-progress `port ∈ torus` authoring capture: whichever side has been
 *  picked (port or torus) shows its label; the other side shows the same `_` placeholder
 *  style as the node/node pending-term preview (renderTerm's awaiting slot). Mirrors
 *  renderPortTorus's committed syntax so the preview reads identically once it commits. */
export function renderPortTorusBuilder(rb: RuleBuilderState) {
  const portSide = rb.pendingPort ? (rb.pendingPort.isInput ? "in" : "out") : null;
  const hasInProgress = rb.pendingPort != null || rb.pendingTorus != null;
  return (
    <>
      <div className="rule-eq-equation">
        <span className="rule-eq-term rule-eq-term--pending">
          (
          <span className="rule-eq-node">{rb.pendingPort ? rb.pendingPort.nodeLabel : "_"}</span>
          ,
          <span className="rule-eq-angle">{rb.pendingPort ? `${portSide}:${rb.pendingPort.label}` : "_"}</span>
          ) ∈ ◯
          <span className="rule-eq-node">{rb.pendingTorus ? rb.pendingTorus.label : "_"}</span>
        </span>
      </div>
      <button
        className="rule-eq-clear"
        disabled={!hasInProgress}
        title="Clear the equation being built"
        onClick={() => postGoRecord(encodeClearRule())}
      >
        Clear
      </button>
    </>
  );
}
