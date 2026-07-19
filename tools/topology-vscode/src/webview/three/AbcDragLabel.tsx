import { createPortal } from "react-dom";
import { useAbcDragCount, useLastAbcDragNodeRow, ABC_DRAG_NO_ROW } from "./overlay-flags";
import { getLatestSnapshot } from "../snapshot-buffer";
import { decodeSnapshot, nodeLabel } from "./buffer-decode";

// AbcDragLabel — a small in-editor affirmation that the time-node abc-drag log is
// happening: a live count of "time.abc-drag" events plus the NAME of the time node
// that most recently received the drag re-quantize, read-only from the content
// buffer's Overlay block (AbcDragCount + LastAbcDragNodeRow columns). No local
// state, no domain authoring — mirrors SpeedSlider's portal-into-toolbar-mount
// pattern, just reading instead of writing.
export function AbcDragLabel() {
  const count = useAbcDragCount();
  const row = useLastAbcDragNodeRow();
  const mount = document.getElementById("abc-drag-mount");
  if (!mount) return null;

  let name = "";
  if (row !== ABC_DRAG_NO_ROW) {
    const snap = getLatestSnapshot();
    const decoded = snap ? decodeSnapshot(snap) : null;
    if (decoded && row < decoded.nodeCount) name = nodeLabel(decoded, row);
  }

  return createPortal(
    <span className="abc-drag-label">
      time drag-log ×{count}
      {name ? ` — ${name}` : ""}
    </span>,
    mount,
  );
}
