// RuleEquationPanel.tsx — symbolic DOM readout of the polar equation the rule-builder is
// currently authoring (gesture.go trySelectSphereRule). Render + forward only: it decodes
// the Go-owned RuleBuilder buffer row (useRuleBuilder) and draws it; it never sets any of
// this state itself. Mounted below the run/pause/stop control panel, visible only while
// the selSpherePoles ("select") overlay is on and there is something to show.

import { createPortal } from "react-dom";
import { useOverlayFlags } from "./overlay-flags";
import { useRuleBuilder, type RuleBuilderTerm } from "./rule-builder";

/** Angle-chip glyphs for the packed term code (matches gesture.go's ruleTermCode: 0=+θ,
 *  1=+φ, 2=−θ, 3=−φ, 4=r — r is unsigned). */
const ANGLE_CHIPS = ["+θ", "+φ", "−θ", "−φ", "r"];

function angleChip(code: number): string {
  return ANGLE_CHIPS[code] ?? "?";
}

export function RuleEquationPanel() {
  const overlays = useOverlayFlags();
  const rb = useRuleBuilder();
  const mount = document.getElementById("rule-eq-mount");
  if (!mount) return null;
  if (!overlays?.selSpherePoles) return null;
  if (!rb) return null;

  // Left term = the first completed term, or (when none completed yet) the pending
  // half-term itself — "show the handhold being selected" before any node is picked.
  const leftTerm = rb.terms[0] ?? null;
  const rightTerm = rb.terms[1] ?? null;
  // The pending half-term slots in wherever a term is still missing: after the left term
  // (awaiting the second handhold) or as the left term itself (nothing completed yet).
  const pendingSlot: "left" | "right" | null =
    rb.pending == null ? null : leftTerm == null ? "left" : rightTerm == null ? "right" : null;

  return createPortal(
    <div className="rule-eq-panel">
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
    </div>,
    mount,
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
