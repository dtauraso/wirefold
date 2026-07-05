// TypedPortTorusForm.tsx — renders the port∈torus typed "+ Add equation" session as the
// equation itself, in the same notation renderPortTorus (CommittedEquationsList) uses:
// `( [portNode] , [portName] ) ∈ ◯ [torusNode]`. Split out of RuleEquationPanel.tsx: a pure
// render function of the form/rb state, threaded the blank-change/selection handlers
// explicitly (all state authority — form/setForm, portAutoCtx, the postGoRecord sends —
// stays in RuleEquationPanel).

import type { KeyboardEvent } from "react";
import type { RuleBuilderState } from "./rule-builder";
import type { PortOption } from "./equation-form";
import { filteredPortOptions, type TypedFormState, PT_PORT_NODE, PT_PORT_NAME, PT_TORUS_NODE } from "./rule-eq-types";
import { renderBlankInput, hint } from "./rule-eq-blank-input";

/** Renders the port∈torus typed session as the equation itself, in the same notation
 *  renderPortTorus uses: `( [portNode] , [portName] ) ∈ ◯ [torusNode]`. The portName blank
 *  keeps its option-list autocomplete under the input. */
export function renderTypedPortTorus(
  f: TypedFormState,
  rb: RuleBuilderState | null,
  onPortTorusNodeBlankChange: (text: string) => void,
  onPortNameBlankChange: (text: string) => void,
  onPortNameKeyDown: (e: KeyboardEvent<HTMLInputElement>) => void,
  selectPortOption: (f: TypedFormState, o: PortOption) => void,
  onBlankEnter: (e: KeyboardEvent<HTMLInputElement>) => void,
) {
  const portNodeCell =
    f.active === PT_PORT_NODE
      ? renderBlankInput(f.text, "node…", "rule-eq-node", onPortTorusNodeBlankChange, onBlankEnter)
      : (rb?.pendingPort?.nodeLabel ?? f.pendingNodeLabel) || hint("node…");
  const opts = filteredPortOptions(f);
  const portNameCell =
    f.active === PT_PORT_NAME ? (
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
    ) : rb?.pendingPort ? (
      `${rb.pendingPort.isInput ? "in" : "out"}:${rb.pendingPort.label}`
    ) : (
      hint("port…")
    );
  const torusNodeCell =
    f.active === PT_TORUS_NODE
      ? renderBlankInput(f.text, "torus…", "rule-eq-node", onPortTorusNodeBlankChange, onBlankEnter)
      : rb?.pendingTorus?.label || hint("torus…");
  return (
    <div className="rule-eq-equation">
      <span className="rule-eq-term">
        (<span className="rule-eq-node">{portNodeCell}</span>,<span className="rule-eq-angle">{portNameCell}</span>)
        {" ∈ ◯ "}
        <span className="rule-eq-node">{torusNodeCell}</span>
      </span>
    </div>
  );
}
