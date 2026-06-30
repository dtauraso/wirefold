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
    handholds: viewerState.handholdsVisible,
    labelsGlobalHidden: viewerState.labelsGlobalHidden,
    badgesHidden: viewerState.badgesHidden,
    overlaysActive: viewerState.overlaysActive,
  };
}

const statusEl = document.getElementById("status");
if (!statusEl) console.warn("save.ts: #status element not found");

let lastViewSyncedText: string | undefined;

// Module-level debounce timer for scene persistence.
let _sceneTimer: ReturnType<typeof setTimeout> | null = null;
const SCENE_SAVE_DEBOUNCE_MS = 400;

export function setStatus(dirty: boolean) {
  if (statusEl) { statusEl.textContent = dirty ? "saving…" : "saved"; statusEl.className = dirty ? "dirty" : "clean"; }
}

export function setStatusError(msg: string) {
  if (statusEl) { statusEl.textContent = `save blocked: ${msg}`; statusEl.className = "dirty"; }
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
  }, SCENE_SAVE_DEBOUNCE_MS);
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
