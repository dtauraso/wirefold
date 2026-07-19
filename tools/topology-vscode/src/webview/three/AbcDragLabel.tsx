import { createPortal } from "react-dom";
import { useAbcDragCount } from "./overlay-flags";
import { getLatestSnapshot } from "../snapshot-buffer";
import { decodeSnapshot, nodeLabel } from "./buffer-decode";
import { readNodeGotDragMsg } from "../../schema/buffer-layout";

// AbcDragLabel — a small in-editor affirmation that the time-node abc-drag log is
// happening: a live count of "time.abc-drag" events plus the NAMES of every node that
// has received at least one drag re-quantize, read-only from the content buffer
// (AbcDragCount Overlay column + the Node block's per-row GotDragMsg flag). The
// recipient set is Go-owned and sticky (accumulates in the buffer, never cleared) —
// this just reads it fresh each frame, no local state, no domain authoring. Mirrors
// SpeedSlider's portal-into-toolbar-mount pattern, just reading instead of writing.
export function AbcDragLabel() {
  const count = useAbcDragCount();
  const mount = document.getElementById("abc-drag-mount");
  if (!mount) return null;

  const names: string[] = [];
  const snap = getLatestSnapshot();
  const decoded = snap ? decodeSnapshot(snap) : null;
  if (decoded) {
    for (let row = 0; row < decoded.nodeCount; row++) {
      if (readNodeGotDragMsg(decoded.nodeView, row)) {
        names.push(nodeLabel(decoded, row));
      }
    }
  }

  return createPortal(
    <span className="abc-drag-label">
      time drag-log ×{count}
      {names.length ? `: ${names.join(", ")}` : ""}
    </span>,
    mount,
  );
}
