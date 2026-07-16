// lifecycle: bundle-eval start — this line runs as soon as the bundle is
// evaluated by the webview, before any React or heavy side effects.
import { vscode } from "./vscode-api";
import { postLog } from "./log/post";
postLog("lifecycle", { phase: "bundle-eval" });

import { createRoot } from "react-dom/client";
import "./webview.css";
import { ThreeView } from "./three/ThreeView";
import { parseHostToWebview } from "../messages";
import { ErrorBoundary } from "./log/ErrorBoundary";
import { CrashListeners } from "./log/CrashListeners";
import { setLatestSnapshot } from "./snapshot-buffer";

function Root() {
  return <ThreeView />;
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
  // buffer-snapshot is the only host→webview message; it is the hot-path firehose and is
  // deliberately never per-message-logged (see the note above).
  if (msg.type === "buffer-snapshot") {
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
