import { viewerState } from "./state/viewer-state";
import { serializeSceneState } from "./state/viewer/types";
import { vscode } from "./vscode-api";

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
  if (lastViewSyncedText === undefined) return;
  const sceneText = serializeSceneState(viewerState);
  if (sceneText === lastViewSyncedText) return;
  lastViewSyncedText = sceneText;
  let scene: unknown;
  try { scene = JSON.parse(sceneText); } catch { return; }
  vscode.postMessage({ type: "edit", op: "scene", scene });
}

export function scheduleViewSave() {
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
