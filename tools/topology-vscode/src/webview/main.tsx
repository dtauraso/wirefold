// lifecycle: bundle-eval start — this line runs as soon as the bundle is
// evaluated by the webview, before any React or heavy side effects.
import { vscode } from "./vscode-api";
import { postLog } from "./log/post";
postLog("lifecycle", { phase: "bundle-eval" });

import { createRoot } from "react-dom/client";
import { useEffect, useState } from "react";
import "./webview.css";
import { ThreeView } from "./three/ThreeView";
import { flushViewSave } from "./save";
import { parseHostToWebview } from "../messages";
import { setRunStatusImperative, registerRunStatusSetter, RunStatusCtx } from "./state/run-status";
import type { RunStatusUI } from "./state/run-status";
import { ErrorBoundary } from "./log/ErrorBoundary";
import { CrashListeners } from "./log/CrashListeners";
import { RunButton } from "./three/RunButton";
import { SaveLifecycle } from "./SaveLifecycle";
import { viewerState } from "./state/viewer-state";
import { setLatestSnapshot } from "./snapshot-buffer";
import { recordNavNodeLabel, clearNavNodeIds } from "./three/buffer-nav";

// Test-only hook for the Playwright e2e harness. The harness stub of
// acquireVsCodeApi populates window.__wirefold_sent with every postMessage
// call from the webview, so a test can assert both the live spec and that a
// save was posted.
(window as unknown as { __wirefold_test: unknown }).__wirefold_test = {
  getSent: () =>
    (window as unknown as { __wirefold_sent?: unknown[] }).__wirefold_sent ?? [],
};

function Root() {
  const [runStatus, setRunStatus] = useState<RunStatusUI>({ state: "idle" });
  useEffect(() => { registerRunStatusSetter(setRunStatus); }, []);
  return (
    <RunStatusCtx.Provider value={runStatus}>
      {/* SaveLifecycle and RunButton are mounted once here for all views. */}
      <SaveLifecycle />
      <RunButton />
      <ThreeView />
    </RunStatusCtx.Provider>
  );
}

postLog("lifecycle", { phase: "before-render" });
const app = document.getElementById("app")!;
createRoot(app).render(
  <ErrorBoundary>
    <CrashListeners />
    <Root />
  </ErrorBoundary>,
);

window.addEventListener("message", (e) => {
  const msg = parseHostToWebview(e.data);
  if (!msg) return;
  postLog("lifecycle", { phase: `msg:${msg.type}` });
  if (msg.type === "run-status") {
    const RUN_STATES = ["running", "paused", "ok", "cancelled"] as const;
    type NonErrorState = typeof RUN_STATES[number];
    setRunStatusImperative(msg.state === "error"
      ? { state: "error", message: msg.message ?? "" }
      : { state: (RUN_STATES as readonly string[]).includes(msg.state)
            ? msg.state
            : "idle" });
  } else if (msg.type === "flush") {
    // Host requests immediate flush of any pending debounced saves (panel
    // becoming hidden / about to dispose).
    flushViewSave();
  } else if (msg.type === "load") {
    // Fully Go/buffer-driven: NO spec store, NO pump. Reset the row-keyed buffer-nav
    // id/label table so it repopulates in node-row order from the fresh run's node-label
    // sidecar. Everything the render needs arrives via buffer-snapshot + node-label;
    // label/badge visibility comes from the buffer overlay columns.
    clearNavNodeIds();
    // Push all Go-owned guide visibilities (including the master overlays toggle) so Go's
    // authoritative state survives a window reload; Go reflects these into the buffer
    // overlay columns the render path reads.
    const guidePush = {
      tori: viewerState.sceneToriVisible !== false,
      scenePoles: viewerState.scenePolesVisible !== false,
      nodePoles: viewerState.nodePolesVisible !== false,
      angleLabels: viewerState.angleLabelsVisible !== false,
      selSpherePoles: viewerState.selSpherePolesVisible !== false,
      handholds: viewerState.handholdsVisible !== false,
      doubleLinks: viewerState.doubleLinksVisible === true,
      // labelsGlobalHidden is hidden sense (true=hidden); labelsGlobal is visible sense.
      labelsGlobal: viewerState.labelsGlobalHidden !== true,
      // badgesHidden is hidden sense (true=hidden); badgesGlobal is visible sense.
      badgesGlobal: viewerState.badgesHidden !== true,
      overlays: viewerState.overlaysActive !== false,
    };
    postLog("guide-load-push", { persisted: {
      sceneTori: viewerState.sceneToriVisible, scenePoles: viewerState.scenePolesVisible,
      nodePoles: viewerState.nodePolesVisible, angleLabels: viewerState.angleLabelsVisible,
      selSpherePoles: viewerState.selSpherePolesVisible, handholdsVisible: viewerState.handholdsVisible, overlaysActive: viewerState.overlaysActive,
    }, pushed: guidePush });
    vscode.postMessage({ type: "edit", op: "update", kind: "overlays", attr: "set", state: guidePush });
  } else if (msg.type === "node-label") {
    // New-system label sidecar: record {id,label} into the row-keyed buffer-nav table
    // (first-seen order == buffer node-row order). Runs under both flags; flag-off never
    // reads this table, so the extra write is invisible there.
    recordNavNodeLabel(msg.id, msg.label, msg.kind);
  } else if (msg.type === "buffer-snapshot") {
    // Phase 3/4: store the latest snapshot for buffer-scene rendering.
    // No render change here — the JSON trace still drives the scene.
    setLatestSnapshot(msg.buffer);
    postLog("buf-snapshot", { byteLength: msg.buffer.byteLength });
  }
  // load for 2D is fully handled inside App's message effect.
});

// Signal readiness after the message listener is registered so the host's
// load response is guaranteed not missed.
vscode.postMessage({ type: "ready" });
postLog("lifecycle", { phase: "ready-sent" });
