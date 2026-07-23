// Message handler for one webview panel. The closure-captured state
// (lastAppliedVersion ref, runner instance, post callback,
// sidecar URI) is passed in via the Ctx struct so this stays a plain
// function rather than a method.

import * as fs from "fs";
import * as path from "path";
import * as vscode from "vscode";
import { BuildAndRunRunner } from "../runCommand";
import {
  parseWebviewToHost,
  type HostToWebviewMsg,
  type WebviewToHostMsg,
} from "../messages";
import { appendWebviewLog } from "./webview-log";
import { PROBE_DIR, PROBE_FILES } from "../probe-files";
import { BUF_BLOCK_TAG_VIEW, BUF_BLOCK_TAG_EDGE_STREAM, BUF_BLOCK_TAG_NODE_STREAM, BUF_BLOCK_TAG_INTERIOR_STREAM } from "../schema/frame-tags";

export type MessageCtx = {
  logUri: vscode.Uri | undefined;
  runner: BuildAndRunRunner;
  post: (msg: HostToWebviewMsg) => void;
};

/** Compile-time exhaustiveness check: if a new WebviewToHostMsg variant is added and
 *  dispatch's switch is not updated to handle it, this call site fails to type-check
 *  (msg is not `never`) instead of the message silently falling off the end of the switch. */
function assertNever(msg: never): never {
  throw new Error(`handle-message: unhandled webview message kind ${JSON.stringify(msg)}`);
}


export async function handleMessage(raw: unknown, ctx: MessageCtx): Promise<void> {
  const msg = parseWebviewToHost(raw);
  if (!msg) {
    console.warn("topology editor: ignoring malformed webview message", raw);
    return;
  }
  try {
    await dispatch(msg, ctx);
  } catch (err) {
    const error = err instanceof Error ? err : new Error(String(err));
    console.error("topology editor: unhandled message handler error", error);

    // Write probe file for post-mortem diagnosis.
    const repoRoot = workspaceRoot();
    if (repoRoot) {
      try {
        const probeDir = path.join(repoRoot, PROBE_DIR);
        fs.mkdirSync(probeDir, { recursive: true });
        const probeFile = path.join(probeDir, PROBE_FILES.handlerErrorLast);
        const entry = JSON.stringify({
          timestamp: new Date().toISOString(),
          msgType: msg.type,
          message: error.message,
          stack: error.stack ?? null,
        });
        fs.appendFileSync(probeFile, entry + "\n", "utf8");
      } catch (probeErr) {
        console.error("topology editor: could not write probe file", probeErr);
      }
    }
  }
}

async function dispatch(msg: WebviewToHostMsg, ctx: MessageCtx): Promise<void> {
  const { logUri, runner } = ctx;
  switch (msg.type) {
    // Every kind in the LIVE_CASES fence below is actually posted by the webview
    // (grep-checked by check-message-kind-parity.sh against literal `postMessage({ type:
    // "..." })` call sites) as well as handled here. A kind with a real handler case but
    // no live sender is exactly the bug class this fence exists to catch (see "run-cancel").
    // LIVE_CASES_START
    case "ready": {
      // Spawn Go immediately so edges render from geometry events before the
      // user presses Run. Go starts HALTED — the clock won't tick until play().
      // A remount (hot-reload after npm run build) leaves Go running but the fresh
      // webview holds no state (see check-no-webview-state.sh) — it has nothing to
      // draw until the next frame Go happens to emit, which may be a long wait while
      // paused/idle. run() is idempotent (no-op if already running).
      // If Go was ALREADY running before this run(), hand the remounted webview EVERY
      // cached last frame (view, plus one per edge/node/interior row) so it renders
      // instantly without round-tripping to Go. A just-spawned Go needs no cached frames —
      // it emits its own startup geometry — so this also dodges any post-spawn
      // stdin-readiness race.
      const wasRunning = runner.isRunning();
      runner.run();
      if (wasRunning) {
        const viewFrame = runner.getLastViewFrame();
        if (viewFrame) {
          ctx.post({ type: "buffer-snapshot", buffer: viewFrame, tag: BUF_BLOCK_TAG_VIEW });
        }
        // Per-edge dedicated streams (see BuildAndRunRunner.getLastEdgeFrames): the
        // per-edge analogue of the loop above — one cached frame per edge row.
        for (const { row, buffer } of runner.getLastEdgeFrames()) {
          ctx.post({ type: "buffer-snapshot", buffer, tag: BUF_BLOCK_TAG_EDGE_STREAM, row });
        }
        // Per-node dedicated streams (see BuildAndRunRunner.getLastNodeFrames /
        // getLastInteriorFrames): the per-node analogue of the edge loop above — one
        // cached frame per node row, for EACH of the two per-node stream kinds (they are
        // written by two different goroutines onto two different fds).
        for (const { row, buffer } of runner.getLastNodeFrames()) {
          ctx.post({ type: "buffer-snapshot", buffer, tag: BUF_BLOCK_TAG_NODE_STREAM, row });
        }
        for (const { row, buffer } of runner.getLastInteriorFrames()) {
          ctx.post({ type: "buffer-snapshot", buffer, tag: BUF_BLOCK_TAG_INTERIOR_STREAM, row });
        }
      }
      return;
    }
    case "webview-log":
      await appendWebviewLog(msg.entry, logUri);
      return;
    case "go-record":
      // BINARY editor→Go bridge: the webview already encoded the raw-input / edit message
      // into a binary record (schema/input-layout.ts). Write it FRAMED to Go's stdin
      // VERBATIM. Fire-and-forget — Go owns the clock; we never await Go (no request/
      // response). The record's layout is decoded + bounds-checked in Go (input_codec.go).
      // Gated on a running Go: writeStdin buffers when proc is null and that buffer flushes
      // onto the NEXT spawned process — which re-reads the graph from disk — so a record
      // sent while Go is stopped could replay/double-apply. Go is always spawned on "ready"
      // before any editor input, so this drops stale records rather than buffering them.
      if (!runner.isRunning()) return;
      runner.writeStdin(msg.record);
      return;
    // LIVE_CASES_END
    // The following kinds are declared in WebviewToHostMsg (and WEBVIEW_TO_HOST_TYPES) so
    // message-kind-parity tracks stdin_reader.go's msg.Type switch, but no live webview code
    // path posts them as a bare JS object: "raw-input"/"edit"/"save" are always
    // encoded into a binary record and sent as "go-record" (see schema/input-layout.ts),
    // never posted directly.
    // If one somehow arrives, this is a bug upstream — log it rather than silently drop it.
    //
    // The ONLY legitimate reason for a kind to sit here is Go-parity: it must be one of the
    // kinds stdin_reader.go's msg.Type switch dispatches (its MSG_TYPES fence). A kind here
    // that Go does not recognize would be dead on both sides and is checked for by
    // check-message-kind-parity.sh — it fails the guard, it does not pass silently.
    // DECLARED_NOT_SENT_START
    case "raw-input":
    case "save":
    case "edit":
      console.warn(`topology editor: unexpected direct "${msg.type}" message (expected via go-record)`, msg);
      return;
    // DECLARED_NOT_SENT_END
    default:
      assertNever(msg);
  }
}

function workspaceRoot(): string | undefined {
  return vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;
}
