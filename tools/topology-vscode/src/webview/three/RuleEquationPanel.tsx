// RuleEquationPanel.tsx — symbolic DOM readout of the polar equation the rule-builder is
// currently authoring (gesture.go trySelectSphereRule). Render + forward only: it decodes
// the Go-owned RuleBuilder buffer row (useRuleBuilder) and draws it; it never sets any of
// this state itself. Mounted below the run/pause/stop control panel, visible only while
// the selSpherePoles ("select") overlay is on and there is something to show.

import { createPortal } from "react-dom";
import { useOverlayFlags } from "./overlay-flags";
import { useRuleBuilder, usePolarLocks, type RuleBuilderTerm, type RuleBuilderState, type PolarLockEntry, POLAR_LOCK_KIND_PORT_TORUS } from "./rule-builder";
import { postGoRecord } from "../vscode-api";
import { encodeClearRule, encodeLockToggleActive, encodeLockSelect, encodeDeleteSelectedLock } from "../../schema/input-layout";
import { useEffect } from "react";

/** Angle-chip glyphs for the packed term code (matches gesture.go's ruleTermCode: 0=θ,
 *  1=φ, 2=−θ, 3=−φ, 4=r — positive θ/φ show no sign). */
const ANGLE_CHIPS = ["θ", "φ", "−θ", "−φ", "r"];

function angleChip(code: number): string {
  return ANGLE_CHIPS[code] ?? "?";
}

export function RuleEquationPanel() {
  const overlays = useOverlayFlags();
  const rb = useRuleBuilder();
  const { equations } = usePolarLocks();
  const mount = document.getElementById("rule-eq-mount");

  // The committed-equations LIST keys off the rule-builder's STICKY panel Center
  // (rb.centerRow, gesture.go md.ruleCenter) rather than the transient click highlight
  // (Node.Selected / useSelectedNodeRow): it shows whenever the sticky center participates
  // in >=1 committed equation, as ANY participant (center, term A, term B, the port's
  // owning node, or the torus). This keeps the panel showing the last-selected node's
  // equations even after an empty-space click clears the highlight ring. The in-progress
  // builder section stays gated on the overlay, as before.
  const centerRow = rb?.centerRow;
  const rowEquations = equations.filter((eq) =>
    eq.kind === POLAR_LOCK_KIND_PORT_TORUS
      ? eq.torusRow === centerRow || eq.portNodeRow === centerRow
      : eq.centerRow === centerRow || eq.a.row === centerRow || eq.b.row === centerRow,
  );
  const showBuilder = !!overlays?.selSpherePoles && !!rb;
  const showList = rowEquations.length > 0;
  // rb.centerLabel tracks md.selected regardless of the overlay (gesture.go applySelect
  // emits it unconditionally now), so show a standalone Center header whenever the list
  // is showing but the builder section (which already renders its own Center line) is not.
  const showListCenter = showList && !showBuilder;

  // Delete key: fires whenever at least one of THIS center's SELECTED rows is deactivated
  // (multi-select — Go deletes every selected+deactivated lock). Go re-guards regardless.
  // Listens while the list is showing.
  useEffect(() => {
    if (!showList) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key !== "Delete" && e.key !== "Backspace") return;
      const hasDeletable = rowEquations.some((eq) => eq.selected && !eq.active);
      if (!hasDeletable) return;
      postGoRecord(encodeDeleteSelectedLock());
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [showList, rowEquations]);

  if (!mount) return null;
  if (!showBuilder && !showList) return null;

  return createPortal(
    <div className="rule-eq-panel">
      {showBuilder && rb && renderBuilder(rb)}
      {showListCenter && <div className="rule-eq-center">Center: {rb?.centerLabel || "—"}</div>}
      {showList && (
        <div className="rule-eq-list">
          {rowEquations.map((eq) => renderLockRow(eq, eq.selected))}
        </div>
      )}
    </div>,
    mount,
  );
}

/** Renders the in-progress equation-being-authored section (the selSpherePoles session).
 *  A `port ∈ torus` authoring capture (rb.pendingPort/rb.pendingTorus) is INDEPENDENT of
 *  the node/node pending term above — if either side is picked, render the port∈torus
 *  in-progress form instead of the node/node builder preview. */
function renderBuilder(rb: RuleBuilderState) {
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

/** Renders the in-progress `port ∈ torus` authoring capture: whichever side has been
 *  picked (port or torus) shows its label; the other side shows the same `_` placeholder
 *  style as the node/node pending-term preview (renderTerm's awaiting slot). Mirrors
 *  renderPortTorus's committed syntax so the preview reads identically once it commits. */
function renderPortTorusBuilder(rb: RuleBuilderState) {
  const portSide = rb.pendingPort ? (rb.pendingPort.isInput ? "in" : "out") : null;
  const hasInProgress = rb.pendingPort != null || rb.pendingTorus != null;
  return (
    <>
      <div className="rule-eq-center">Center: {rb.centerLabel || "—"}</div>
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
        {eq.kind === POLAR_LOCK_KIND_PORT_TORUS
          ? renderPortTorus(eq)
          : (
            <>
              {renderTerm({ row: eq.a.row, label: eq.a.label, code: eq.a.code }, null)}
              <span className="rule-eq-op"> = </span>
              {renderTerm({ row: eq.b.row, label: eq.b.label, code: eq.b.code }, null)}
            </>
          )}
      </span>
    </div>
  );
}

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

/** Renders one term slot: a completed term (filled), a pending half-term (node slot
 *  empty, angle chip highlighted — "show the handhold being selected"), or nothing. */
function renderTerm(term: RuleBuilderTerm | null, pendingCode: number | null) {
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
