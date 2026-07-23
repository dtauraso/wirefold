// lifecycle: bundle-eval start — this line runs as soon as the bundle is
// evaluated by the webview, before any React or heavy side effects.
import { vscode } from "./vscode-api";
import { postLog } from "./log/post";
postLog("lifecycle", { phase: "bundle-eval" });

import { createRoot } from "react-dom/client";
import "./webview.css";
import { ThreeView } from "./three/ThreeView";
import { SpeedSlider } from "./three/SpeedSlider";
import { AbcDragLabel } from "./three/AbcDragLabel";
import { parseHostToWebview } from "../messages";
import { ErrorBoundary } from "./log/ErrorBoundary";
import { CrashListeners } from "./log/CrashListeners";
import { setLatestSnapshot, setLatestBeadFrame, setLatestNodeFrame, setLatestEdgeFrame, setLatestViewFrame, setLatestEdgeStreamFrame } from "./snapshot-buffer";
import { BUF_BLOCK_TAG_SCENE, BUF_BLOCK_TAG_BEAD, BUF_BLOCK_TAG_NODE, BUF_BLOCK_TAG_EDGE, BUF_BLOCK_TAG_VIEW, BUF_BLOCK_TAG_EDGE_STREAM } from "../schema/frame-tags";

function Root() {
  return (
    <>
      <ThreeView />
      <SpeedSlider />
      <AbcDragLabel />
    </>
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
  // buffer-snapshot is the only host→webview message; it is the hot-path firehose and is
  // deliberately never per-message-logged (see the note above).
  if (msg.type === "buffer-snapshot") {
    // Route by block tag (schema/frame-tags.ts): the SCENE frame updates the scene cell
    // (buffer-scene rendering), the BEAD frame updates its own separate cell
    // (BeadInstances.tsx), the NODE frame updates its own separate cell (the node/
    // port/interior/label renderers), the EDGE frame updates its own separate cell
    // (EdgeTube.tsx), and the VIEW frame (camera+overlay+scene, arriving on its own
    // dedicated pipe when active — see runCommand.ts's VIEW_FD) updates its own separate
    // cell (three/view-blocks.ts) — none of those four ride the scene frame anymore when
    // the view fd is active. EVERY frame is applied — nothing dropped. The observability
    // log is COALESCED to at most ~1/sec (with a running count) so it does not re-flood
    // the webview->host bridge and re-starve raw-input.
    if (msg.tag === BUF_BLOCK_TAG_BEAD) {
      setLatestBeadFrame(msg.buffer);
    } else if (msg.tag === BUF_BLOCK_TAG_NODE) {
      setLatestNodeFrame(msg.buffer);
    } else if (msg.tag === BUF_BLOCK_TAG_EDGE) {
      setLatestEdgeFrame(msg.buffer);
    } else if (msg.tag === BUF_BLOCK_TAG_VIEW) {
      setLatestViewFrame(msg.buffer);
    } else if (msg.tag === BUF_BLOCK_TAG_EDGE_STREAM) {
      // One of the per-edge dedicated streams (row names WHICH edge — see
      // snapshot-buffer.ts's edgeStreamFrames map). Dropped (not applied) if row is
      // missing — a malformed relay, never emitted by a correct ext host.
      if (typeof msg.row === "number") {
        setLatestEdgeStreamFrame(msg.row, msg.buffer);
      }
    } else if (msg.tag === BUF_BLOCK_TAG_SCENE) {
      setLatestSnapshot(msg.buffer);
    }
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
