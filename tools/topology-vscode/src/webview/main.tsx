// lifecycle: bundle-eval start — this line runs as soon as the bundle is
// evaluated by the webview, before any React or heavy side effects.
import { vscode } from "./vscode-api";
import { postLog } from "./log/post";
postLog("lifecycle", { phase: "bundle-eval" });

import { createRoot } from "react-dom/client";
import { useEffect, useState } from "react";
import "./webview.css";
import { ThreeView } from "./three/ThreeView";
import { parseHostToWebview } from "../messages";
import { setRunStatusImperative, registerRunStatusSetter, RunStatusCtx } from "./state/run-status";
import type { RunStatusUI } from "./state/run-status";
import { ErrorBoundary } from "./log/ErrorBoundary";
import { CrashListeners } from "./log/CrashListeners";
import { RunButton } from "./three/RunButton";
import { setLatestSnapshot } from "./snapshot-buffer";

function Root() {
  const [runStatus, setRunStatus] = useState<RunStatusUI>({ state: "idle" });
  useEffect(() => { registerRunStatusSetter(setRunStatus); }, []);
  return (
    <RunStatusCtx.Provider value={runStatus}>
      {/* RunButton is mounted once here for all views. */}
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

// Coalesced buffer-snapshot logging state (see the buffer-snapshot case below): at most one
// observability log per ~second carrying the count since the last log, instead of one per
// snapshot. Snapshots themselves are never coalesced — all are applied.
let bufSnapLogAt = 0;
let bufSnapCount = 0;

window.addEventListener("message", (e) => {
  const msg = parseHostToWebview(e.data);
  if (!msg) return;
  // NEVER per-message-log the buffer-snapshot firehose. Go emits a full-state snapshot
  // per trace-event (~430–700/sec during animation); logging each one (here + the
  // byteLength log below) posted ~2 webview-log messages PER snapshot back to the host,
  // ~860/sec on the SAME webview->host bridge that carries raw-input pointermoves — and
  // each triggered a host-side file append. That starved orbit input (camera lagged while
  // beads, computed in Go and delivered outbound, stayed smooth). Snapshots are NOT dropped
  // — every one is still applied below; only the hot-path logging is suppressed/coalesced.
  if (msg.type !== "buffer-snapshot") {
    postLog("lifecycle", { phase: `msg:${msg.type}` });
  }
  if (msg.type === "run-status") {
    const RUN_STATES = ["active", "ok", "cancelled"] as const;
    type NonErrorState = typeof RUN_STATES[number];
    setRunStatusImperative(msg.state === "error"
      ? { state: "error", message: msg.message ?? "" }
      : { state: (RUN_STATES as readonly string[]).includes(msg.state)
            ? msg.state
            : "idle" });
  } else if (msg.type === "buffer-snapshot") {
    // Store the latest snapshot for buffer-scene rendering. EVERY snapshot is applied —
    // nothing dropped. The observability log is COALESCED to at most ~1/sec (with a running
    // count) so it does not re-flood the webview->host bridge and re-starve raw-input.
    setLatestSnapshot(msg.buffer);
    bufSnapCount += 1;
    const now = Date.now();
    if (now - bufSnapLogAt >= 1000) {
      postLog("buf-snapshot", { byteLength: msg.buffer.byteLength, sinceLast: bufSnapCount, windowMs: now - bufSnapLogAt });
      bufSnapLogAt = now;
      bufSnapCount = 0;
    }
  }
});

// Signal readiness after the message listener is registered.
vscode.postMessage({ type: "ready" });
postLog("lifecycle", { phase: "ready-sent" });
