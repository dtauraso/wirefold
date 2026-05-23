// Message handler for one webview panel. The closure-captured state
// (lastAppliedVersion ref, runner instance, post callback,
// sidecar URI) is passed in via the Ctx struct so this stays a plain
// function rather than a method.

import * as cp from "child_process";
import * as path from "path";
import * as vscode from "vscode";
import { BuildAndRunRunner } from "../runCommand";
import { extractViewText, injectViewText } from "../sidecar";
import { parseWebviewToHost, type HostToWebviewMsg, type WebviewToHostMsg } from "../messages";
import { applyEdit } from "./html";
import { appendWebviewLog } from "./webview-log";
import { toErrorMessage } from "../utils/error";

export type MessageCtx = {
  document: vscode.TextDocument;
  runner: BuildAndRunRunner;
  post: (msg: HostToWebviewMsg) => Thenable<boolean>;
  send: () => Thenable<boolean>;
  sendView: () => Promise<unknown>;
  setLastAppliedVersion: (v: number) => void;
};

export async function handleMessage(raw: unknown, ctx: MessageCtx): Promise<void> {
  const msg = parseWebviewToHost(raw);
  if (!msg) {
    console.warn("topology editor: ignoring malformed webview message", raw);
    return;
  }
  await dispatch(msg, ctx);
}

async function dispatch(msg: WebviewToHostMsg, ctx: MessageCtx): Promise<void> {
  const { document, runner, post } = ctx;
  switch (msg.type) {
    case "ready":
      ctx.send();
      await ctx.sendView();
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
    case "delivered":
      runner.writeStdin(JSON.stringify({ type: "delivered", edge: msg.edge }));
      return;
    case "pseudo-render":
      await handlePseudoRender(msg.nodeId, document, post);
      return;
    case "pseudo-save":
      await handlePseudoSave(msg.nodeId, msg.pseudo, document, post);
      return;
  }
}

// ── Pseudo helpers ────────────────────────────────────────────────────────────

function workspaceRoot(): string | undefined {
  return vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;
}

function findNodeSpec(docText: string, nodeId: string): Record<string, unknown> | undefined {
  let parsed: unknown;
  try { parsed = JSON.parse(docText); } catch { return undefined; }
  const nodes = (parsed as { nodes?: unknown[] }).nodes;
  if (!Array.isArray(nodes)) return undefined;
  const node = nodes.find((n: unknown) => (n as { id?: string }).id === nodeId);
  if (!node) return undefined;
  return (node as { data?: Record<string, unknown> }).data ?? {};
}

function spawnGoRun(repoRoot: string, args: string[]): Promise<{ stdout: string; stderr: string; code: number }> {
  return new Promise((resolve) => {
    const proc = cp.spawn("go", ["run", "./cmd/pseudo", ...args], { cwd: repoRoot });
    let stdout = "";
    let stderr = "";
    proc.stdout.on("data", (d: Buffer) => { stdout += d.toString(); });
    proc.stderr.on("data", (d: Buffer) => { stderr += d.toString(); });
    proc.on("close", (code) => resolve({ stdout, stderr, code: code ?? 1 }));
  });
}

async function handlePseudoRender(
  nodeId: string,
  document: vscode.TextDocument,
  post: (msg: HostToWebviewMsg) => Thenable<boolean>,
): Promise<void> {
  const repoRoot = workspaceRoot();
  if (!repoRoot) {
    post({ type: "pseudo-error", nodeId, message: "no workspace folder" });
    return;
  }
  const specEntry = findNodeSpec(document.getText(), nodeId);
  if (!specEntry) {
    post({ type: "pseudo-error", nodeId, message: `node ${nodeId} not found in topology` });
    return;
  }
  const goFile = path.join(repoRoot, "nodes", "Input", "node.go");
  const { stdout, stderr, code } = await spawnGoRun(repoRoot, [
    "input", "render",
    "--go-file", goFile,
    "--spec-json", JSON.stringify(specEntry),
  ]);
  if (code !== 0) {
    let msg = stderr.trim();
    try { msg = (JSON.parse(msg) as { error?: string }).error ?? msg; } catch { /* use raw */ }
    post({ type: "pseudo-error", nodeId, message: msg });
    return;
  }
  post({ type: "pseudo-render-result", nodeId, pseudo: stdout.trimEnd() });
}

async function handlePseudoSave(
  nodeId: string,
  pseudoText: string,
  document: vscode.TextDocument,
  post: (msg: HostToWebviewMsg) => Thenable<boolean>,
): Promise<void> {
  const repoRoot = workspaceRoot();
  if (!repoRoot) {
    post({ type: "pseudo-error", nodeId, message: "no workspace folder" });
    return;
  }
  const specEntry = findNodeSpec(document.getText(), nodeId);
  if (!specEntry) {
    post({ type: "pseudo-error", nodeId, message: `node ${nodeId} not found in topology` });
    return;
  }
  const goFile = path.join(repoRoot, "nodes", "Input", "node.go");
  const { stdout, stderr, code } = await spawnGoRun(repoRoot, [
    "input", "save",
    "--go-file", goFile,
    "--spec-json", JSON.stringify(specEntry),
    "--pseudo", pseudoText,
  ]);
  if (code !== 0) {
    let msg = stderr.trim();
    try { msg = (JSON.parse(msg) as { error?: string }).error ?? msg; } catch { /* use raw */ }
    post({ type: "pseudo-error", nodeId, message: msg });
    return;
  }
  let result: { go: string; spec: Record<string, unknown> };
  try {
    result = JSON.parse(stdout) as typeof result;
  } catch (e) {
    post({ type: "pseudo-error", nodeId, message: `could not parse cmd/pseudo output: ${toErrorMessage(e)}` });
    return;
  }

  // Write new Go source.
  const goUri = vscode.Uri.file(goFile);
  await vscode.workspace.fs.writeFile(goUri, Buffer.from(result.go, "utf8"));

  // Patch the node's data in topology.json and save.
  let topologyParsed: unknown;
  try { topologyParsed = JSON.parse(document.getText()); } catch (e) {
    post({ type: "pseudo-error", nodeId, message: `could not parse topology: ${toErrorMessage(e)}` });
    return;
  }
  const topo = topologyParsed as { nodes?: { id: string; data?: Record<string, unknown> }[] };
  if (Array.isArray(topo.nodes)) {
    const node = topo.nodes.find((n) => n.id === nodeId);
    if (node) {
      node.data = { ...(node.data ?? {}), ...result.spec };
    }
  }
  await applyEdit(document, JSON.stringify(topologyParsed, null, 2));
  await document.save();
  post({ type: "pseudo-save-result", nodeId });
}
