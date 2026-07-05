// port-autocomplete-context.tsx — ephemeral cross-tree UI signal: "the equation-form's
// portName autocomplete is open for buffer node row N, row H is highlighted". This is NOT
// domain state (Go owns no such concept and is never asked about it) — it exists only so the
// DOM-mounted RuleEquationPanel (which node/port the user is typing) can tell the
// Canvas-mounted PortLabels layer which port name labels to draw and which one to emphasize.
// Plain React context + useState in Root (main.tsx); not a store, not a buffer subscription.

import { createContext, useContext } from "react";

export interface PortAutocompleteUI {
  /** Buffer node row whose ports should show name labels. */
  nodeRow: number;
  /** Buffer port row currently highlighted in the autocomplete list, or -1 for none. */
  highlightedRow: number;
}

export interface PortAutocompleteCtxValue {
  value: PortAutocompleteUI | null;
  setValue: (v: PortAutocompleteUI | null) => void;
}

const noop = () => {};

export const PortAutocompleteContext = createContext<PortAutocompleteCtxValue>({
  value: null,
  setValue: noop,
});

export function usePortAutocompleteContext(): PortAutocompleteCtxValue {
  return useContext(PortAutocompleteContext);
}
