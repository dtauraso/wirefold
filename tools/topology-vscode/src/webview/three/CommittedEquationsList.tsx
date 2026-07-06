// CommittedEquationsList.tsx — the committed polar-equation rows for the panel's sticky
// Center (rb.centerRow): activate/deactivate checkbox + the symbolic equation, in either the
// (node,comp)=(node,comp) tuple form or the port∈torus membership form. Split out of
// RuleEquationPanel.tsx: pure render of already-decoded PolarLockEntry rows, no local state.

import { postGoRecord } from "../vscode-api";
import { encodeLockToggleActive, encodeLockSelect } from "../../schema/input-layout";
import { POLAR_LOCK_KIND_PORT_TORUS, type PolarLockEntry } from "./rule-builder";
import { renderTerm } from "./RuleEquationBuilderPreview";

/** Renders a `port ∈ torus` membership lock: (nodeLabel,side) ∈ ◯torusLabel. Rendered
 *  distinctly from the (node,comp)=(node,comp) tuple form above — there is no equals sign,
 *  this is a membership relation, not an equation between two terms. STAGE 1 display only
 *  (no geometric effect). */
function renderPortTorus(eq: PolarLockEntry) {
  const side = eq.portIsInput ? "in" : "out";
  return (
    <span className="rule-eq-term">
      (<span className="rule-eq-node">{eq.portNodeLabel || "?"}</span>,{side}:{eq.portLabel || "?"}) ∈ ◯
      <span className="rule-eq-node">{eq.torusLabel || "?"}</span>
    </span>
  );
}

/** Renders one committed polar-equation lock row: activate/deactivate checkbox + the
 *  symbolic equation. Clicking the row (not the checkbox) focuses it (edit-update
 *  lock/selected); the checkbox toggles active (edit-update lock/active). */
function renderLockRow(eq: PolarLockEntry, selected: boolean) {
  const cls = ["rule-eq-row", selected ? "rule-eq-row--selected" : "", eq.active ? "" : "rule-eq-row--inactive"]
    .filter(Boolean)
    .join(" ");
  return (
    <div
      key={eq.index}
      className={cls}
      onClick={() => postGoRecord(encodeLockSelect(eq.index))}
    >
      <input
        type="checkbox"
        checked={eq.active}
        onClick={(e) => e.stopPropagation()}
        onChange={() => postGoRecord(encodeLockToggleActive(eq.index))}
      />
      <span className="rule-eq-equation">
        {eq.kind === POLAR_LOCK_KIND_PORT_TORUS
          ? renderPortTorus(eq)
          : (
            <>
              {renderTerm({ row: eq.a.row, label: eq.a.label, code: eq.a.code }, null, eq.centerLabel)}
              <span className="rule-eq-op"> = </span>
              {renderTerm({ row: eq.b.row, label: eq.b.label, code: eq.b.code }, null, eq.centerLabel)}
            </>
          )}
      </span>
    </div>
  );
}

/** The committed-equations list for the panel's sticky Center — one renderLockRow per
 *  row already filtered (by RuleEquationPanel) to the ones the sticky center participates
 *  in. Thin wrapper so RuleEquationPanel's JSX stays a plain component call. */
export function CommittedEquationsList({ rowEquations }: { rowEquations: PolarLockEntry[] }) {
  return (
    <div className="rule-eq-list">
      {rowEquations.map((eq) => renderLockRow(eq, eq.selected))}
    </div>
  );
}
