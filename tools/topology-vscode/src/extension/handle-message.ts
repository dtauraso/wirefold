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
    case "pause":
      runner.pause();
      return;
    case "resume":
      runner.play();
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
    // LIVE_CASES_END
    // The following kinds are declared in WebviewToHostMsg (and WEBVIEW_TO_HOST_TYPES) so
    // message-kind-parity tracks stdin_reader.go's msg.Type switch, but no live webview code
    // path posts them as a bare JS object: "resend" is host-originated only (never sent by
    // the webview); "raw-input"/"edit"/"save"/"fade-toggle" are always encoded into a binary
    // record and sent as "go-record" (see schema/input-layout.ts), never posted directly;
    // "play" is declared only so this union tracks Go's binary-record "play" kind — the
    // ext-host builds that record itself (BuildAndRunRunner.play(), invoked by the "run"
    // and "resume" cases above), so no webview code ever posts a bare {type:"play"}.
    // If one somehow arrives, this is a bug upstream — log it rather than silently drop it.
    //
    // The ONLY legitimate reason for a kind to sit here is Go-parity: it must be one of the
    // kinds stdin_reader.go's msg.Type switch dispatches (its MSG_TYPES fence). A kind here
    // that Go does not recognize would be dead on both sides and is checked for by
    // check-message-kind-parity.sh — it fails the guard, it does not pass silently.
    // DECLARED_NOT_SENT_START
    case "resend":
    case "raw-input":
    case "save":
    case "fade-toggle":
    case "play":
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
