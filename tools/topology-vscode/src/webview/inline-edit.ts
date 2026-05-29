// In-place contenteditable editor for node sublabels.
// Uses the existing label element directly so the edited text is visually
// identical to the rendered label without a positioning round-trip.

import { scheduleViewSave } from "./save";
import { useThreeStore } from "./three/store";

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
  // When initial is empty, leave whatever was visibly rendered (e.g. the
  // "+ sublabel" placeholder) in place so the pill doesn't collapse. The
  // select-all below means typing still replaces it; callers detect the
  // unreplaced placeholder string on commit and treat it as empty.
  if (opts.initial !== "") el.textContent = opts.initial;

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
  const rfNode = useThreeStore.getState().nodes.find((n) => n.id === nodeId);
  const saved = rfNode?.data?.sublabel as string | undefined;
  const pseudo = rfNode?.data?.pseudo as string | undefined;
  const visible = (el?.textContent ?? "").trim();
  const placeholderText = "+ sublabel";
  const fromVisible = visible === placeholderText ? "" : visible;
  const original = saved ?? pseudo ?? fromVisible;
  beginInlineEdit(el, {
    initial: original,
    activeClass: "sublabel-active",
    onCommit: (rawNext) => {
      // If the user never replaced the visible placeholder, treat as empty.
      const next = rawNext === placeholderText ? "" : rawNext;
      if (next === original) return "";
      useThreeStore.getState().setNodes((ns) => ns.map((n) => {
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
