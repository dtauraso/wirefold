// lifecycle: bundle-eval start — this line runs as soon as the bundle is
// evaluated by the webview, before any React or heavy side effects.
import { vscode } from "./vscode-api";
import { postLog } from "./log/post";
postLog("lifecycle", { phase: "bundle-eval" });

// Early crash listeners — fire BEFORE React mounts so blank-window crashes
// are captured. These run synchronously here; CrashListeners (mounted inside
// the React tree) duplicates coverage once the tree is up and removes these.
{
  const earlyOnError = (e: ErrorEvent) => {
    postLog("early-window-error", {
      message: e.message,
      filename: e.filename,
      lineno: e.lineno,
      colno: e.colno,
      stack: (e.error as Error | undefined)?.stack ?? "",
    });
  };
  const earlyOnRejection = (e: PromiseRejectionEvent) => {
    const reason = e.reason as { message?: string; stack?: string } | undefined;
    postLog("early-unhandled-rejection", {
      message: reason?.message ?? String(e.reason),
      stack: reason?.stack ?? "",
    });
  };
  window.addEventListener("error", earlyOnError);
  window.addEventListener("unhandledrejection", earlyOnRejection);
}

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
import { useThreeStore } from "./three/store";
import { handleTraceEvent } from "./three/pump";
import { viewerState } from "./state/viewer-state";
import { useCameraStore } from "./three/camera-store";

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
    setRunStatusImperative(msg.state === "error"
      ? { state: "error", message: msg.message ?? "" }
      : { state: msg.state as "running" | "paused" | "ok" | "cancelled" });
  } else if (msg.type === "flush") {
    // Host requests immediate flush of any pending debounced saves (panel
    // becoming hidden / about to dispose).
    flushViewSave();
  } else if (msg.type === "load") {
    // Feed the R3F store; topology.json text carries spec + diagram view;
    // sceneText (optional) carries camera/camera3d/labelsGlobalHidden from topology.scene.json.
    useThreeStore.getState().load(msg.text, msg.sceneText);
    // Re-seed the guidelines master toggle from persisted state, then push all 5
    // Go-owned guide visibilities so Go's authoritative state survives a window reload.
    useCameraStore.getState().setGuidelinesActive(viewerState.guidelinesActive !== false);
    const guidePush = {
      tori: viewerState.sceneToriVisible !== false,
      scenePoles: viewerState.scenePolesVisible !== false,
      nodePoles: viewerState.nodePolesVisible !== false,
      angleLabels: viewerState.angleLabelsVisible !== false,
      selSpherePoles: viewerState.selSpherePolesVisible !== false,
      handholds: viewerState.handholdsVisible !== false,
    };
    postLog("guide-load-push", { persisted: {
      sceneTori: viewerState.sceneToriVisible, scenePoles: viewerState.scenePolesVisible,
      nodePoles: viewerState.nodePolesVisible, angleLabels: viewerState.angleLabelsVisible,
      selSpherePoles: viewerState.selSpherePolesVisible, handholdsVisible: viewerState.handholdsVisible, guidelinesActive: viewerState.guidelinesActive,
    }, pushed: guidePush });
    vscode.postMessage({ type: "edit", op: "guide-vis", ...guidePush });
  } else if (msg.type === "trace-event") {
    handleTraceEvent(msg.event);
  }
  // load for 2D is fully handled inside App's message effect.
});

// Signal readiness after the message listener is registered so the host's
// load response is guaranteed not missed.
vscode.postMessage({ type: "ready" });
postLog("lifecycle", { phase: "ready-sent" });
