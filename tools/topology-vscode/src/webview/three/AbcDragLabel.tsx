import { createPortal } from "react-dom";
import { useAbcDragCount, useAbcDragRows } from "./overlay-flags";

// AbcDragLabel — the in-editor "drag received" log. A header line carrying a live
// count of abc-drag events, then ONE LINE PER RECIPIENT: that node's name and the
// delta triple (dA,dB,dC) it received. The delta is the DRAGGED node's own
// quantized-triple change, computed once at the drag and carried on the neighborSetC
// message — a recipient reports the delta it was handed, it does not apply it.
//
// All read-only from the content buffer (AbcDragCount Overlay column + the Node
// block's per-row GotDragMsg flag and DragDeltaA/B/C columns), via the buffer-reflect
// hooks in overlay-flags.ts. Go-owned and drag-scoped: Go clears the set at drag start
// (KindAbcDragReset) and emits the cleared state, so an empty list is meaningful and
// must render. The count alone is NOT enough to drive this — the drag-start clear does
// not move the count, which is why the rows come through their own hook.
//
// Node names live ONLY on the per-recipient lines, never duplicated into the header:
// the header is the log's name line, the rows are the data.
// No local state, no domain authoring. Mirrors SpeedSlider's portal-into-toolbar-mount
// pattern, just reading instead of writing.
export function AbcDragLabel() {
  const count = useAbcDragCount();
  const rows = useAbcDragRows();
  const mount = document.getElementById("abc-drag-mount");
  if (!mount) return null;

  return createPortal(
    <span className="abc-drag-label">
      <span className="abc-drag-label-header">drag received ×{count}</span>
      {rows.map((r) => (
        <span className="abc-drag-label-row" key={r.name}>
          {r.name}: ({r.dA}, {r.dB}, {r.dC})
        </span>
      ))}
    </span>,
    mount,
  );
}
