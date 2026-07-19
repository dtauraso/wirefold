import { createPortal } from "react-dom";
import { useAbcDragCount } from "./overlay-flags";

// AbcDragLabel — a small in-editor affirmation that the time-node abc-drag log is
// happening: a live count of "time.abc-drag" events, read-only from the content
// buffer's Overlay block (AbcDragCount column). No local state, no domain
// authoring — mirrors SpeedSlider's portal-into-toolbar-mount pattern, just reading
// instead of writing.
export function AbcDragLabel() {
  const count = useAbcDragCount();
  const mount = document.getElementById("abc-drag-mount");
  if (!mount) return null;

  return createPortal(
    <span className="abc-drag-label">time drag-log ×{count}</span>,
    mount,
  );
}
