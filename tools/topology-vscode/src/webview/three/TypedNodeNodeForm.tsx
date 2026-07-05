// TypedNodeNodeForm.tsx — renders the node=node typed "+ Add equation" session as the
// equation itself, in parens notation: `Center: [center]` then
// `( [nodeA] , [compA] ) = ( [nodeB] , [compB] )`. Split out of RuleEquationPanel.tsx: a pure
// render function of the form/rb state, threaded the blank-change handlers explicitly (all
// state authority — form/setForm, the postGoRecord sends — stays in RuleEquationPanel).

import type { KeyboardEvent } from "react";
import type { RuleBuilderState } from "./rule-builder";
import { compLivePreview } from "./equation-form";
import { angleChip, NN_CENTER, NN_NODE_A, NN_COMP_A, NN_NODE_B, NN_COMP_B, type TypedFormState } from "./rule-eq-types";
import { renderBlankInput, hint } from "./rule-eq-blank-input";

/** Renders the node=node typed session as the equation itself, in parens notation:
 *  `Center: [center]` then `( [nodeA] , [compA] ) = ( [nodeB] , [compB] )`. Resolved blanks
 *  come from rb (the same Go-streamed state a click drives); the blank currently being typed
 *  is an inline input; a term's node blank shows its buffered (not-yet-sent) value once the
 *  form has moved on to that term's comp blank; unreached blanks show `_`. */
export function renderTypedNodeNode(
  f: TypedFormState,
  rb: RuleBuilderState | null,
  onNodeBlankChange: (text: string) => void,
  onCompBlankChange: (text: string) => void,
  onBlankEnter: (e: KeyboardEvent<HTMLInputElement>) => void,
) {
  const centerLabel =
    f.active === NN_CENTER
      ? renderBlankInput(f.text, "node…", "rule-eq-node", onNodeBlankChange, onBlankEnter)
      : rb?.centerLabel || hint("node…");
  const termA = rb?.terms[0] ?? null;
  const termB = rb?.terms[1] ?? null;
  const nodeACell =
    f.active === NN_NODE_A
      ? renderBlankInput(f.text, "node…", "rule-eq-node", onNodeBlankChange, onBlankEnter)
      : (termA?.label ?? (f.active > NN_NODE_A ? f.pendingNodeLabel : hint("node…")));
  const compACell =
    f.active === NN_COMP_A
      ? renderBlankInput(compLivePreview(f.text), "θ/φ/r", "rule-eq-angle", onCompBlankChange, onBlankEnter)
      : (termA != null ? angleChip(termA.code) : hint("θ/φ/r"));
  const nodeBCell =
    f.active === NN_NODE_B
      ? renderBlankInput(f.text, "node…", "rule-eq-node", onNodeBlankChange, onBlankEnter)
      : (termB?.label ?? (f.active > NN_NODE_B ? f.pendingNodeLabel : hint("node…")));
  const compBCell =
    f.active === NN_COMP_B
      ? renderBlankInput(compLivePreview(f.text), "θ/φ/r", "rule-eq-angle", onCompBlankChange, onBlankEnter)
      : (termB != null ? angleChip(termB.code) : hint("θ/φ/r"));
  return (
    <>
      <div className="rule-eq-center">Center: {centerLabel}</div>
      <div className="rule-eq-equation">
        <span className="rule-eq-term">
          (<span className="rule-eq-node">{nodeACell}</span>,<span className="rule-eq-angle">{compACell}</span>)
        </span>
        <span className="rule-eq-op"> = </span>
        <span className="rule-eq-term">
          (<span className="rule-eq-node">{nodeBCell}</span>,<span className="rule-eq-angle">{compBCell}</span>)
        </span>
      </div>
    </>
  );
}
