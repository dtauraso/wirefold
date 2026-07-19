import { createPortal } from "react-dom";
import { useAbcDragCount, useAbcDragNames } from "./overlay-flags";

// AbcDragLabel — a small in-editor affirmation that the abc-drag log is
// happening: a live count of "abc-drag" events plus the NAMES of every node that
// received a drag re-quantize during the CURRENT drag, read-only from the content
// buffer (AbcDragCount Overlay column + the Node block's per-row GotDragMsg flag).
// Both are Go-owned; the name set is drag-scoped — Go clears it at drag start
// (KindAbcDragReset) and emits the cleared state, so an empty list is meaningful and
// must render. Both inputs are read via the buffer-reflect hooks in overlay-flags.ts: the
// count alone is NOT enough to drive this, because the drag-start clear does not move
// the count.
// No local state, no domain authoring. Mirrors SpeedSlider's portal-into-toolbar-mount
// pattern, just reading instead of writing.
export function AbcDragLabel() {
  const count = useAbcDragCount();
  const names = useAbcDragNames();
  const mount = document.getElementById("abc-drag-mount");
  if (!mount) return null;

  return createPortal(
    <span className="abc-drag-label">
      drag-log ×{count}
      {names.length ? `: ${names.join(", ")}` : ""}
    </span>,
    mount,
  );
}
