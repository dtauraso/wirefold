// Message handler for one webview panel. The closure-captured state
// (lastAppliedVersion ref, runner instance, post callback,
// sidecar URI) is passed in via the Ctx struct so this stays a plain
// function rather than a method.

import * as fs from "fs";
import * as path from "path";
import * as vscode from "vscode";
import { BuildAndRunRunner } from "../runCommand";
import { extractViewText, injectViewText } from "../sidecar";
import {
  parseWebviewToHost,
  type HostToWebviewMsg,
  type WebviewToHostMsg,
} from "../messages";
import { applyEdit } from "./html";
import { appendWebviewLog } from "./webview-log";
import { toErrorMessage } from "../utils/error";

export type MessageCtx = {
  document: vscode.TextDocument;
  runner: BuildAndRunRunner;
  post: (msg: HostToWebviewMsg) => Thenable<boolean>;
  send: () => Thenable<boolean>;
  setLastAppliedVersion: (v: number) => void;
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
  const { document, runner, post } = ctx;
  switch (msg.type) {
    case "ready":
      ctx.send();
      return;
    case "save":
      try {
        const viewText = extractViewText(document.getText());
        const merged = viewText ? injectViewText(msg.text, viewText) : msg.text;
        ctx.setLastAppliedVersion(document.version + 1);
        await applyEdit(document, merged);
        await document.save();
        ctx.setLastAppliedVersion(document.version);
      } catch (err) {
        post({ type: "save-error", message: toErrorMessage(err) });
      }
      return;
    case "view-save":
      try {
        const merged = injectViewText(document.getText(), msg.text);
        ctx.setLastAppliedVersion(document.version + 1);
        await applyEdit(document, merged);
        await document.save();
        ctx.setLastAppliedVersion(document.version);
      }
      catch (err) { post({ type: "save-error", message: toErrorMessage(err) }); }
      return;
    case "run":
      try {
        if (msg.text !== undefined) {
          const viewText = extractViewText(document.getText());
          const merged = viewText ? injectViewText(msg.text, viewText) : msg.text;
          ctx.setLastAppliedVersion(document.version + 1);
          await applyEdit(document, merged);
          await document.save();
          ctx.setLastAppliedVersion(document.version);
        }
      } catch (err) {
        console.error("topology editor: run pre-write failed", err);
        post({ type: "save-error", message: toErrorMessage(err) });
        return;
      }
      runner.run();
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
      await appendWebviewLog(msg.entry, document.uri);
      return;
    case "edit":
      // Single geometry-CRUD bridge: forward the edit to Go's stdin verbatim by op.
      // Fire-and-forget — Go owns the clock; we never await Go (no request/response).
      // The create/delete breadcrumb log is awaited BEFORE the write (diagnostics
      // only); the writeStdin send itself is non-blocking. z defaults to 0.
      if (msg.op === "create" || msg.op === "delete") {
        await appendWebviewLog(JSON.stringify({ ts_ms: Date.now(), src: "ts-ext", label: `edit-${msg.op}-forward`, target: msg.target, targetHandle: msg.targetHandle }), document.uri);
        runner.writeStdin(JSON.stringify({ type: "edit", op: msg.op, target: msg.target, targetHandle: msg.targetHandle }));
      } else if (msg.op === "update") {
        runner.writeStdin(JSON.stringify({ type: "edit", op: "update", nodeId: msg.nodeId, x: msg.x, y: msg.y, z: msg.z ?? 0 }));
      } else if (msg.op === "fade") {
        runner.writeStdin(JSON.stringify({ type: "edit", op: "fade", edges: msg.edges }));
      }
      return;
  }
}

function workspaceRoot(): string | undefined {
  return vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;
}
