import { viewerState } from "./state/viewer-state";
import { serializeSceneState } from "./state/viewer/types";
import { postGoRecord } from "./vscode-api";
import { encodeSave } from "../schema/input-layout";
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

let lastViewSyncedText: string | undefined;

// Module-level debounce timer for scene persistence.
let _sceneTimer: ReturnType<typeof setTimeout> | null = null;
const SCENE_SAVE_DEBOUNCE_MS = 400;

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
  // The scene text is computed ONLY to detect a change (dedupe/debounce) — it is NOT sent.
  // Go owns the authoritative scene state (camera pose + overlay visibility) and persists
  // ITS OWN current state on this bare command. No document crosses the bridge.
  postLog("scene-save-send", { posted: true, guides: guideSnapshot(), sceneText });
  postGoRecord(encodeSave());
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
