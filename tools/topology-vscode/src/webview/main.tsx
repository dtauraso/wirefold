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
import "reactflow/dist/style.css";
import "./webview.css";
import { ThreeView } from "./three/ThreeView";
import { flushSave, flushViewSave } from "./save";
import { parseHostToWebview } from "../messages";
import { flowToSpec } from "./rf/adapter/flow-to-spec";
import { setRunStatusImperative, registerRunStatusSetter, RunStatusCtx } from "./rf/run-status";
import type { RunStatusUI } from "./rf/run-status";
import { setDimmedImperative } from "./rf/dimmed";
import { ErrorBoundary } from "./log/ErrorBoundary";
import { CrashListeners } from "./log/CrashListeners";
import { RunButton } from "./rf/panels/RunButton";
import { SaveLifecycle } from "./SaveLifecycle";
import { useThreeStore } from "./three/store";
import { handleTraceEvent } from "./rf/pump";

// Test-only hook for the Playwright e2e harness. The harness stub of
// acquireVsCodeApi populates window.__wirefold_sent with every postMessage
// call from the webview, so a test can assert both the live spec and that a
// save was posted.
(window as unknown as { __wirefold_test: unknown }).__wirefold_test = {
  getSpec: () => { const { nodes, edges } = useThreeStore.getState(); return flowToSpec(nodes, edges, { nodes: [], edges: [] }); },
  getSent: () =>
    (window as unknown as { __wirefold_sent?: unknown[] }).__wirefold_sent ?? [],
  // Test-only: drive the dim state directly so tests don't need to click
  // the saved-views panel. Pass a string[] of member ids to dim everything
  // *not* in the set; pass undefined to clear.
  applyDim: (members: string[] | undefined) =>
    setDimmedImperative(members ? new Set(members) : null),
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
    flushSave();
    flushViewSave();
  } else if (msg.type === "load") {
    // Feed the R3F store independently of the 2D RF handler.
    useThreeStore.getState().loadSpec(msg.text);
  } else if (msg.type === "view-load") {
    // Feed the R3F store with viewer-state independently of the 2D RF handler.
    useThreeStore.getState().loadView(msg.text);
  } else if (msg.type === "trace-event") {
    handleTraceEvent(msg.event);
  }
  // load/view-load for 2D is fully handled inside App's message effect.
});

// Signal readiness after the message listener is registered so the host's
// response (load + view-load) is guaranteed not missed.
vscode.postMessage({ type: "ready" });
postLog("lifecycle", { phase: "ready-sent" });
