import { createRoot } from "react-dom/client";
import { useEffect, useState } from "react";
import "reactflow/dist/style.css";
import "./webview.css";
import { ThreeView } from "./three/ThreeView";
import { flushSave, flushViewSave } from "./save";
import { parseHostToWebview } from "../messages";
import { rfGetNodes, rfGetEdges } from "./rf/rf-imperative";
import { flowToSpec } from "./rf/adapter/flow-to-spec";
import { setRunStatusImperative, registerRunStatusSetter, RunStatusCtx } from "./rf/run-status";
import type { RunStatusUI } from "./rf/run-status";
import { setDimmedImperative } from "./rf/dimmed";
import { ErrorBoundary } from "./log/ErrorBoundary";
import { CrashListeners } from "./log/CrashListeners";
import { RunButton } from "./rf/panels/RunButton";
import { SaveLifecycle } from "./SaveLifecycle";
import { useThreeStore } from "./three/store";

// Test-only hook for the Playwright e2e harness. The harness stub of
// acquireVsCodeApi populates window.__wirefold_sent with every postMessage
// call from the webview, so a test can assert both the live spec and that a
// save was posted.
(window as unknown as { __wirefold_test: unknown }).__wirefold_test = {
  getSpec: () => flowToSpec(rfGetNodes(), rfGetEdges(), { nodes: [], edges: [] }),
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
  }
  // load/view-load for 2D is fully handled inside App's message effect.
});
