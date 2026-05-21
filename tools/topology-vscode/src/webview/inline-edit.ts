// In-place contenteditable editor for node sublabels.
// Uses the existing label element directly so the edited text is visually
// identical to the rendered label without a positioning round-trip.

import { scheduleViewSave } from "./save";
import { rfSetNodes, rfGetNodes } from "./rf/rf-imperative";
import { pushSnapshot } from "./rf/history";

type RerenderFn = () => void;

let active: HTMLElement | null = null;
let rerender: RerenderFn = () => {};
export function setInlineEditRerender(fn: RerenderFn) { rerender = fn; }

// Synchronously commit any in-progress inline edit. Run / save-flush
// callsites use this so a half-typed rename doesn't get left out of the
// posted spec. Returns true if an edit was active and committed.
export function flushActiveInlineEdit(): boolean {
  if (!active) return false;
  active.blur();
  return true;
}

interface Options {
  initial: string;             // text shown in the editor; written back on cancel
  activeClass: string;         // CSS class added during edit (visual cue)
  onCommit: (next: string) => string | null;
  // null = accepted; string = rejected with an error message. The element's
  // text content is restored to `initial` on rejection. The implementation
  // is responsible for actually applying the edit via RF state.
}

function beginInlineEdit(el: HTMLElement | null, opts: Options) {
  if (active || !el) return;
  active = el;
  el.classList.add(opts.activeClass, "nodrag", "nopan");
  el.contentEditable = "plaintext-only";
  el.spellcheck = false;
  el.textContent = opts.initial;

  // Select all text so typing replaces the existing label.
  const range = document.createRange();
  range.selectNodeContents(el);
  const sel = window.getSelection();
  sel?.removeAllRanges();
  sel?.addRange(range);
  el.focus();

  let done = false;
  const finish = (commit: boolean) => {
    if (done) return;
    done = true;
    const next = (el.textContent ?? "").trim();
    el.contentEditable = "false";
    el.classList.remove(opts.activeClass, "nodrag", "nopan");
    active = null;
    if (commit) {
      const err = opts.onCommit(next);
      if (err !== null && err !== "") window.alert(err);
    }
    // Always rerender from RF state so cancelled edits / rejected commits /
    // no-op commits all restore whatever decorated DOM the renderer would
    // produce (e.g. sublabel placeholder italics for an empty value).
    rerender();
  };

  el.addEventListener("keydown", (ev: KeyboardEvent) => {
    if (ev.key === "Enter") { ev.preventDefault(); finish(true); }
    else if (ev.key === "Escape") { ev.preventDefault(); finish(false); }
  });
  el.addEventListener("blur", () => finish(true), { once: true });
}

export function beginEditSublabel(nodeId: string, el: HTMLElement | null) {
  // Read current sublabel from RF node data.
  const rfNode = rfGetNodes().find((n) => n.id === nodeId);
  const original = (rfNode?.data?.sublabel as string | undefined) ?? "";
  beginInlineEdit(el, {
    initial: original,
    activeClass: "sublabel-active",
    onCommit: (next) => {
      if (next === original) return "";
      pushSnapshot();
      rfSetNodes((ns) => ns.map((n) => {
        if (n.id !== nodeId) return n;
        const data = { ...n.data };
        if (next === "") {
          delete data.sublabel;
        } else {
          data.sublabel = next;
        }
        return { ...n, data };
      }));
      scheduleViewSave();
      return null;
    },
  });
}
