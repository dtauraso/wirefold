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

export type MessageCtx = {
  logUri: vscode.Uri | undefined;
  runner: BuildAndRunRunner;
  post: (msg: HostToWebviewMsg) => void;
};


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
    case "ready": {
      // Spawn Go immediately so edges render from geometry events before the
      // user presses Run. Go starts HALTED — the clock won't tick until play().
      // A remount (hot-reload after npm run build) resets the webview's module-level
      // edge-geometry store but leaves Go running; run() is then idempotent (no-op),
      // so geometry is never re-streamed and edges vanish until Reload Window.
      // If Go was ALREADY running before this run(), request a geometry resend so the
      // remounted webview rebuilds its store. A just-spawned Go needs no resend — it
      // emits startup geometry on its own — so this also dodges any post-spawn
      // stdin-readiness race (we only send resend when stdin was already live).
      const wasRunning = runner.isRunning();
      runner.run();
      if (wasRunning) runner.resend();
      return;
    }
    case "run":
      // Primary path: Go is already spawned on open (case "ready") and the user is
      // starting the clock for the first time, or resuming after a stop+restart.
      // runner.run() is idempotent (no-op if already running), so it is safe to call
      // unconditionally before play().
      runner.run();
      runner.play();
      return;
    case "play":
      runner.play();
      return;
    case "run-cancel":
      runner.cancel();
      return;
    case "pause":
      runner.pause();
      return;
    case "resume":
      runner.resume();
      return;
    case "stop":
      runner.stop();
      return;
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
  }
}

function workspaceRoot(): string | undefined {
  return vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;
}
