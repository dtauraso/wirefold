// Pure rename logic, separated from the DOM-driven editor in rename.ts so
// unit tests can exercise it without standing up the webview.

import type { Spec } from "../../../schema";
import type { ViewerState } from "../viewer/types";

export const IDENT_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;

// Mutates spec and viewerState in place. Returns an error string if the
// rename is rejected, or null on success.
export function applyRename(
  spec: Spec,
  viewerState: ViewerState,
  oldId: string,
  newId: string,
): string | null {
  if (!IDENT_RE.test(newId)) {
    return `"${newId}" is not a valid Go identifier ([A-Za-z_][A-Za-z0-9_]*)`;
  }
  if (spec.nodes.some((n) => n.id === newId)) {
    return `node id "${newId}" already exists`;
  }
  const node = spec.nodes.find((n) => n.id === oldId);
  if (!node) return `node "${oldId}" not found`;

  node.id = newId;
  for (const e of spec.edges) {
    if (e.source === oldId) e.source = newId;
    if (e.target === oldId) e.target = newId;
  }
  if (viewerState.lastSelectionIds) {
    viewerState.lastSelectionIds = viewerState.lastSelectionIds.map(
      (x) => (x === oldId ? newId : x),
    );
  }
  return null;
}
