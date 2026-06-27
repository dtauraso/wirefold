import { viewerState } from "./state/viewer-state";
import { serializeSceneState } from "./state/viewer/types";
import { vscode } from "./vscode-api";
import { postLog } from "./log/post";

// guideSnapshot — the 6 guideline settings currently held in viewerState, for diagnostic
// logging of the save path (undefined = default true/active).
function guideSnapshot() {
  return {
    sceneTori: viewerState.sceneToriVisible,
    scenePoles: viewerState.scenePolesVisible,
    nodePoles: viewerState.nodePolesVisible,
    angleLabels: viewerState.angleLabelsVisible,
    selSpherePoles: viewerState.selSpherePolesVisible,
    guidelinesActive: viewerState.guidelinesActive,
  };
}

const status = document.getElementById("status")!;

let lastViewSyncedText: string | undefined;

// Module-level debounce timer for scene persistence.
let _sceneTimer: ReturnType<typeof setTimeout> | null = null;

export function setStatus(dirty: boolean) {
  status.textContent = dirty ? "saving…" : "saved";
  status.className = dirty ? "dirty" : "clean";
}

export function setStatusError(msg: string) {
  status.textContent = `save blocked: ${msg}`;
  status.className = "dirty";
}

function _sendScene() {
  if (lastViewSyncedText === undefined) {
    postLog("scene-save-send", { skipped: "not-synced-yet", guides: guideSnapshot() });
    return;
  }
  const sceneText = serializeSceneState(viewerState);
  if (sceneText === lastViewSyncedText) {
    postLog("scene-save-send", { skipped: "unchanged", guides: guideSnapshot() });
    return;
  }
  lastViewSyncedText = sceneText;
  let scene: unknown;
  try { scene = JSON.parse(sceneText); } catch { return; }
  postLog("scene-save-send", { posted: true, guides: guideSnapshot(), sceneText });
  vscode.postMessage({ type: "edit", op: "scene", scene });
}

export function scheduleViewSave() {
  postLog("scene-save-schedule", { gated: lastViewSyncedText === undefined, guides: guideSnapshot() });
  if (lastViewSyncedText === undefined) return;
  if (_sceneTimer !== null) clearTimeout(_sceneTimer);
  _sceneTimer = setTimeout(() => {
    _sceneTimer = null;
    _sendScene();
  }, 400);
}

export function flushViewSave() {
  if (_sceneTimer !== null) {
    clearTimeout(_sceneTimer);
    _sceneTimer = null;
  }
  _sendScene();
}

// Build the guard key from a viewer state's scene fields.
// Must match the key used in store.ts markViewSynced so initial load does not retrigger a save.
export function viewSyncedKey(s: import("./state/viewer/types").ViewerState): string {
  return serializeSceneState(s);
}

export function markViewSynced(text: string) {
  lastViewSyncedText = text;
}
