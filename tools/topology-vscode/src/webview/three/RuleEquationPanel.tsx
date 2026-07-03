// RuleEquationPanel.tsx — symbolic DOM readout of the polar equation the rule-builder is
// currently authoring (gesture.go trySelectSphereRule). Render + forward only: it decodes
// the Go-owned RuleBuilder buffer row (useRuleBuilder) and draws it; it never sets any of
// this state itself. Mounted below the run/pause/stop control panel, visible only while
// the selSpherePoles ("select") overlay is on and there is something to show.

import { createPortal } from "react-dom";
import { useOverlayFlags } from "./overlay-flags";
import { useRuleBuilder, usePolarLocks, useSelectedNodeRow, type RuleBuilderTerm, type PolarLockEntry } from "./rule-builder";
import { postGoRecord } from "../vscode-api";
import { encodeClearRule, encodeLockToggleActive, encodeLockSelect, encodeDeleteSelectedLock } from "../../schema/input-layout";
import { useEffect } from "react";

/** Angle-chip glyphs for the packed term code (matches gesture.go's ruleTermCode: 0=+θ,
 *  1=+φ, 2=−θ, 3=−φ, 4=r — r is unsigned). */
const ANGLE_CHIPS = ["+θ", "+φ", "−θ", "−φ", "r"];

function angleChip(code: number): string {
  return ANGLE_CHIPS[code] ?? "?";
}

export function RuleEquationPanel() {
  const overlays = useOverlayFlags();
  const rb = useRuleBuilder();
  const { equations, selectedLockIndex } = usePolarLocks();
  const selectedRow = useSelectedNodeRow();
  const mount = document.getElementById("rule-eq-mount");

  // The committed-equations LIST is independent of the selSpherePoles overlay: it shows
  // whenever the selected node is the Center of >=1 committed equation. The in-progress
  // builder section stays gated on the overlay, as before.
  const rowEquations = equations.filter((eq) => eq.centerRow === selectedRow);
  const showBuilder = !!overlays?.selSpherePoles && !!rb;
  const showList = rowEquations.length > 0;

  // Delete key: only when the panel-focused row is one of THIS center's rows and is
  // deactivated. Go re-guards regardless. Listens while the list is showing.
  useEffect(() => {
    if (!showList) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key !== "Delete" && e.key !== "Backspace") return;
      const focused = rowEquations.find((eq) => eq.index === selectedLockIndex);
      if (!focused || focused.active) return;
      postGoRecord(encodeDeleteSelectedLock());
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [showList, rowEquations, selectedLockIndex]);

  if (!mount) return null;
  if (!showBuilder && !showList) return null;

  return createPortal(
    <div className="rule-eq-panel">
      {showBuilder && rb && renderBuilder(rb)}
      {showList && (
        <div className="rule-eq-list">
          {rowEquations.map((eq) => renderLockRow(eq, eq.index === selectedLockIndex))}
        </div>
      )}
    </div>,
    mount,
  );
}

/** Renders the in-progress equation-being-authored section (the selSpherePoles session). */
function renderBuilder(rb: { centerLabel: string; pending: { code: number } | null; terms: RuleBuilderTerm[] }) {
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
      <div className="rule-eq-center">Center: {rb.centerLabel || "—"}</div>
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
        Clear equation
      </button>
    </>
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
        {renderTerm({ row: eq.a.row, label: eq.a.label, code: eq.a.code }, null)}
        <span className="rule-eq-op"> = </span>
        {renderTerm({ row: eq.b.row, label: eq.b.label, code: eq.b.code }, null)}
      </span>
    </div>
  );
}

/** Renders one term slot: a completed term (filled), a pending half-term (node slot
 *  empty, angle chip highlighted — "show the handhold being selected"), or nothing. */
function renderTerm(term: RuleBuilderTerm | null, pendingCode: number | null) {
  if (term != null) {
    return (
      <span className="rule-eq-term">
        ( <span className="rule-eq-node">{term.label}</span>{" "}
        <span className="rule-eq-angle">{angleChip(term.code)}</span> )
      </span>
    );
  }
  if (pendingCode != null) {
    return (
      <span className="rule-eq-term rule-eq-term--pending">
        ( <span className="rule-eq-node rule-eq-node--awaiting">_</span>{" "}
        <span className="rule-eq-angle rule-eq-angle--pending">{angleChip(pendingCode)}</span> )
      </span>
    );
  }
  return null;
}
