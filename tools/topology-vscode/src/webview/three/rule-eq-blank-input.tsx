// rule-eq-blank-input.tsx — the two smallest presentational primitives shared by both typed
// equation forms (node=node and port∈torus): the inline `<input>` standing in for an
// unresolved blank, and the dim placeholder hint shown for blanks not yet reached. Split out
// of RuleEquationPanel.tsx so TypedNodeNodeForm/TypedPortTorusForm can share them.

import type { KeyboardEvent } from "react";

/** Renders an inline `<input>` sitting where a blank's value would otherwise be printed —
 *  same rule-eq-node/rule-eq-angle classes as the resolved/awaiting spans, so the active
 *  blank visually matches the rest of the equation. */
export function renderBlankInput(
  text: string,
  placeholder: string,
  cls: string,
  onChange: (t: string) => void,
  onKeyDown?: (e: KeyboardEvent<HTMLInputElement>) => void,
) {
  return (
    <input
      autoFocus
      className={"rule-eq-blank-input " + cls}
      size={Math.max(placeholder.length, text.length, 2)}
      value={text}
      placeholder={placeholder}
      onChange={(e) => onChange(e.target.value)}
      onKeyDown={onKeyDown}
    />
  );
}

/** A dim placeholder shown in a blank that isn't filled yet (and isn't the active input),
 *  so every field advertises what it expects (node… / θ/φ/r / port… / torus…) at all times. */
export function hint(t: string) {
  return <span className="rule-eq-hint">{t}</span>;
}
