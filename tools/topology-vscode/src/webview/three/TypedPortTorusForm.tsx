// TypedPortTorusForm.tsx — renders the port∈torus typed "+ Add equation" session as the
// equation itself, in the same notation renderPortTorus (CommittedEquationsList) uses:
// `( [centerNode] , [portName] ) ∈ ◯ [centerNode]`. The torus is ALWAYS the port's own
// node — the sticky Center — so both the port-node and torus slots are PRESET, display-only
// (derived from rb.centerLabel / f.pendingNodeLabel), never typed; the single blank is the
// port name (with its option-list autocomplete under the input). Split out of
// RuleEquationPanel.tsx: a pure render function of the form/rb state, threaded the
// blank-change/selection handlers explicitly (all state authority — form/setForm,
// portAutoCtx, the postGoRecord sends — stays in RuleEquationPanel).

import type { KeyboardEvent } from "react";
import type { RuleBuilderState } from "./rule-builder";
import type { PortOption } from "./equation-form";
import { filteredPortOptions, type TypedFormState } from "./rule-eq-types";
import { hint } from "./rule-eq-blank-input";

/** Renders the port∈torus typed session as the equation itself, in the same notation
 *  renderPortTorus uses: `( [centerNode] , [portName] ) ∈ ◯ [centerNode]`. Both node slots
 *  are preset to the sticky Center (never a free node input); the portName blank keeps its
 *  option-list autocomplete under the input. */
export function renderTypedPortTorus(
  f: TypedFormState,
  rb: RuleBuilderState | null,
  onPortNameBlankChange: (text: string) => void,
  onPortNameKeyDown: (e: KeyboardEvent<HTMLInputElement>) => void,
  selectPortOption: (f: TypedFormState, o: PortOption) => void,
) {
  const centerLabel = f.pendingNodeLabel || rb?.centerLabel || "";
  const centerCell = centerLabel || hint("node…");
  const opts = filteredPortOptions(f);
  const portNameCell = (
    <span className="rule-eq-typed-input-wrap">
      <input
        autoFocus
        className="rule-eq-blank-input rule-eq-angle"
        size={Math.max(4, f.text.length)}
        value={f.text}
        placeholder="port…"
        onChange={(e) => onPortNameBlankChange(e.target.value)}
        onKeyDown={onPortNameKeyDown}
      />
      <span className="rule-eq-form-autocomplete">
        {opts.map((o, oi) => (
          <span
            key={o.row}
            className={"rule-eq-form-option" + (oi === f.portHighlight ? " rule-eq-form-option--hl" : "")}
            onMouseDown={(e) => {
              e.preventDefault();
              selectPortOption(f, o);
            }}
          >
            {o.isInput ? "in" : "out"}:{o.name}
          </span>
        ))}
      </span>
    </span>
  );
  return (
    <div className="rule-eq-equation">
      <span className="rule-eq-term">
        (<span className="rule-eq-node">{centerCell}</span>,<span className="rule-eq-angle">{portNameCell}</span>)
        {" ∈ ◯ "}
        <span className="rule-eq-node">{centerCell}</span>
      </span>
    </div>
  );
}
