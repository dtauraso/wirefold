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

export type MessageCtx = {
  logUri: vscode.Uri;
  runner: BuildAndRunRunner;
  post: (msg: HostToWebviewMsg) => Thenable<boolean>;
  send: () => Thenable<boolean>;
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
        const probeDir = path.join(repoRoot, ".probe");
        fs.mkdirSync(probeDir, { recursive: true });
        const probeFile = path.join(probeDir, "handler-error-last.json");
        const entry = JSON.stringify({
          timestamp: new Date().toISOString(),
          msgType: msg.type,
          nodeId: (msg as { nodeId?: string }).nodeId ?? null,
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
  const { logUri, runner, post } = ctx;
  switch (msg.type) {
    case "ready": {
      ctx.send();
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
    case "edit":
      // Single geometry-CRUD bridge: forward the edit to Go's stdin verbatim by op.
      // Fire-and-forget — Go owns the clock; we never await Go (no request/response).
      // The create/delete breadcrumb log is awaited BEFORE the write (diagnostics
      // only); the writeStdin send itself is non-blocking. z defaults to 0.
      if (msg.op === "create" || msg.op === "delete") {
        await appendWebviewLog(JSON.stringify({ ts_ms: Date.now(), src: "ts-ext", label: `edit-${msg.op}-forward`, target: msg.target, targetHandle: msg.targetHandle }), logUri);
        runner.writeStdin(JSON.stringify({ type: "edit", op: msg.op, target: msg.target, targetHandle: msg.targetHandle }));
      } else if (msg.op === "update") {
        // Forward the node-move entries map verbatim (keyed by moved node id + each
        // incident edge id); Go's stdin reader mail-sorts each entry to the owning
        // node/edge goroutine. Fire-and-forget.
        runner.writeStdin(JSON.stringify({ type: "edit", op: "update", entries: msg.entries }));
      } else if (msg.op === "fade") {
        // edges is Record<string, boolean>: edgeId → desired faded state. Forward verbatim.
        runner.writeStdin(JSON.stringify({ type: "edit", op: "fade", edges: msg.edges }));
      } else if (msg.op === "port-anchor") {
        // Move a port along its node's ring. node/port identify the port, isInput
        // selects input vs output list, anchor is the new direction offset, keys lists
        // the routing keys (node id + each incident edge id) Go mail-sorts to. Forward
        // verbatim, fire-and-forget — same fan-out shape as op="update".
        runner.writeStdin(JSON.stringify({ type: "edit", op: "port-anchor", node: msg.node, port: msg.port, isInput: msg.isInput, anchor: msg.anchor, keys: msg.keys }));
      } else if (msg.op === "scene") {
        runner.writeStdin(JSON.stringify({ type: "edit", op: "scene", scene: msg.scene }));
      } else if (msg.op === "set-origin") {
        // Re-base the polar frame to the camera's new pan focus. Fire-and-forget.
        runner.writeStdin(JSON.stringify({ type: "edit", op: "set-origin", x: msg.x, y: msg.y, z: msg.z }));
      } else if (msg.op === "viewpoint") {
        // Polar camera nav: forward the viewpoint payload verbatim. Fire-and-forget.
        runner.writeStdin(JSON.stringify({ type: "edit", op: "viewpoint", viewpoint: msg.viewpoint }));
      } else if (msg.op === "tori-vis") {
        // Toggle polar-guide tori visibility. No payload — Go owns the toggle state.
        runner.writeStdin(JSON.stringify({ type: "edit", op: "tori-vis" }));
      }
      return;
  }
}

function workspaceRoot(): string | undefined {
  return vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;
}
