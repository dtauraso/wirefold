import { viewerState } from "./rf/viewer-state";
import { serializeViewerState } from "./state/viewer/types";
import type { Spec } from "../schema";
import { vscode } from "./vscode-api";
import { rfGetNodes, rfGetEdges } from "./rf/rf-imperative";
import { flowToSpec } from "./rf/adapter/flow-to-spec";

const status = document.getElementById("status")!;

// Top-level spec metadata that flowToSpec passes through verbatim.
// Updated on each load so saves preserve fields that aren't carried in RF node/edge data.
let _specMeta: Spec = { nodes: [], edges: [] };
export function setSpecMeta(s: Spec) { _specMeta = s; };

let lastViewSyncedText: string | undefined;

// Debounced timing lives in <SaveLifecycle /> (useDebouncedCallback).
// schedule()/flush() bridge module-level callers to the component's
// debouncer; null until the component mounts.
type Saver = { schedule: () => void; flush: () => void };
let saveImpl: Saver | null = null;
let viewSaveImpl: Saver | null = null;

export function registerSavers(save: Saver, viewSave: Saver) {
  saveImpl = save;
  viewSaveImpl = viewSave;
}

export function setStatus(dirty: boolean) {
  status.textContent = dirty ? "saving…" : "saved";
  status.className = dirty ? "dirty" : "clean";
}

// Pure send-now helpers — invoked by the debouncer in <SaveLifecycle />
// after the trailing edge fires, or directly via flushSave/flushViewSave.
export function performSave() {
  const nodes = rfGetNodes();
  const edges = rfGetEdges();
  // Derive spec from RF state. If RF is not yet mounted (nodes/edges empty),
  // there is nothing to save — skip silently.
  if (nodes.length === 0 && edges.length === 0) return;
  const derived = flowToSpec(nodes, edges, _specMeta);
  const text = JSON.stringify(derived, null, 2) + "\n";
  vscode.postMessage({ type: "save", text });
  setStatus(false);
}

export function performViewSave() {
  // Race guard: until view-load has been processed (markViewSynced called),
  // viewerState lacks the persisted nodes/edges from the sidecar. Saving in
  // that window serializes empty {nodes,edges} and clobbers the file on
  // disk. See task/view-load-race-guard.
  if (lastViewSyncedText === undefined) return;
  const text = serializeViewerState(viewerState);
  if (text === lastViewSyncedText) return;
  lastViewSyncedText = text;
  vscode.postMessage({ type: "view-save", text });
}

export function scheduleSave() {
  setStatus(true);
  saveImpl?.schedule();
}

export function flushSave() {
  saveImpl?.flush();
}

export function scheduleViewSave() {
  if (lastViewSyncedText === undefined) return;
  viewSaveImpl?.schedule();
}

export function flushViewSave() {
  viewSaveImpl?.flush();
}

export function markViewSynced(text: string) {
  lastViewSyncedText = text;
}
