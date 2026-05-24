// Message handler for one webview panel. The closure-captured state
// (lastAppliedVersion ref, runner instance, post callback,
// sidecar URI) is passed in via the Ctx struct so this stays a plain
// function rather than a method.

import * as cp from "child_process";
import * as path from "path";
import * as vscode from "vscode";
import { BuildAndRunRunner } from "../runCommand";
import { extractViewText, injectViewText } from "../sidecar";
import {
  parseWebviewToHost,
  pseudoMsgTypes,
  PSEUDO_KIND_PREFIX,
  PSEUDO_PREFIX_TO_KIND,
  ALL_PSEUDO_RENDER_TYPES,
  ALL_PSEUDO_SAVE_TYPES,
  type PseudoKind,
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
    default: {
      // Data-driven pseudo dispatch: matches render/save types for all registered PseudoKinds.
      type RenderFn = (nodeId: string, document: vscode.TextDocument, post: (msg: HostToWebviewMsg) => Thenable<boolean>) => Promise<void>;
      type SaveFn   = (nodeId: string, pseudo: string, document: vscode.TextDocument, runner: BuildAndRunRunner, post: (msg: HostToWebviewMsg) => Thenable<boolean>) => Promise<void>;
      const pseudoTable: Record<PseudoKind, { render: RenderFn; save: SaveFn }> = {
        Input:    { render: handlePseudoRender,    save: handlePseudoSave },
        ReadGate: { render: handleReadgateRender,  save: handleReadgateSave },
      };
      if (ALL_PSEUDO_RENDER_TYPES.has(msg.type)) {
        const prefix = msg.type.slice(0, -"-render".length) as import("../messages").PseudoPrefix;
        const kind   = PSEUDO_PREFIX_TO_KIND[prefix];
        await pseudoTable[kind].render((msg as { nodeId: string }).nodeId, document, post);
        return;
      }
      if (ALL_PSEUDO_SAVE_TYPES.has(msg.type)) {
        const prefix = msg.type.slice(0, -"-save".length) as import("../messages").PseudoPrefix;
        const kind   = PSEUDO_PREFIX_TO_KIND[prefix];
        await pseudoTable[kind].save((msg as { nodeId: string; pseudo: string }).nodeId, (msg as { pseudo: string }).pseudo, document, runner, post);
        return;
      }
    }
  }
}

// ── ReadGate helpers ──────────────────────────────────────────────────────────

// Mirrors nodes/readgate ToChainInhibitor output port; extension/webview boundary prevents importing the Go const.
const READGATE_OUT_HANDLE = "ToChainInhibitor";

function findOutNeighbor(docText: string, nodeId: string): string | undefined {
  let parsed: unknown;
  try { parsed = JSON.parse(docText); } catch { return undefined; }
  const edges = (parsed as { edges?: unknown[] }).edges;
  if (!Array.isArray(edges)) return undefined;
  const edge = edges.find(
    (e: unknown) =>
      (e as { source?: string }).source === nodeId &&
      (e as { sourceHandle?: string }).sourceHandle === READGATE_OUT_HANDLE,
  );
  if (!edge) return undefined;
  return (edge as { target?: string }).target;
}

async function handleReadgateRender(
  nodeId: string,
  document: vscode.TextDocument,
  post: (msg: HostToWebviewMsg) => Thenable<boolean>,
): Promise<void> {
  const m = pseudoMsgTypes("readgate");
  const repoRoot = workspaceRoot();
  if (!repoRoot) {
    post({ type: m.error, nodeId, message: "no workspace folder" });
    return;
  }
  const outNeighbor = findOutNeighbor(document.getText(), nodeId);
  if (!outNeighbor) {
    post({ type: m.error, nodeId, message: `node ${nodeId} has no ${READGATE_OUT_HANDLE} edge` });
    return;
  }
  const goFile = path.join(repoRoot, "nodes", "readgate", "node.go");
  const { stdout, stderr, code } = await spawnGoRun(repoRoot, [
    "readgate", "render",
    "--go-file", goFile,
    "--out-neighbor", outNeighbor,
  ]);
  if (code !== 0) {
    let msg = stderr.trim();
    try { msg = (JSON.parse(msg) as { error?: string }).error ?? msg; } catch { /* use raw */ }
    post({ type: m.error, nodeId, message: msg });
    return;
  }
  post({ type: m.renderResult, nodeId, pseudo: stdout.trimEnd() });
}

async function handleReadgateSave(
  nodeId: string,
  pseudoText: string,
  document: vscode.TextDocument,
  runner: BuildAndRunRunner,
  post: (msg: HostToWebviewMsg) => Thenable<boolean>,
): Promise<void> {
  const m = pseudoMsgTypes("readgate");
  const repoRoot = workspaceRoot();
  if (!repoRoot) {
    post({ type: m.error, nodeId, message: "no workspace folder" });
    return;
  }
  const currentNeighbor = findOutNeighbor(document.getText(), nodeId);
  if (!currentNeighbor) {
    post({ type: m.error, nodeId, message: `node ${nodeId} has no ${READGATE_OUT_HANDLE} edge` });
    return;
  }
  const goFile = path.join(repoRoot, "nodes", "readgate", "node.go");
  const { stdout, stderr, code } = await spawnGoRun(repoRoot, [
    "readgate", "save",
    "--go-file", goFile,
    "--out-neighbor", currentNeighbor,
    "--pseudo", pseudoText,
  ]);
  if (code !== 0) {
    const raw = stderr.trim();
    let msg = raw;
    let suggestion = "";
    try {
      const parsed = JSON.parse(raw) as { error?: string; suggestion?: string };
      msg = parsed.error ?? raw;
      suggestion = parsed.suggestion ?? "";
    } catch { /* use raw */ }
    post({ type: m.error, nodeId, message: msg, suggestion });
    return;
  }
  let result: { go: string; outNeighbor: string; removedPorts: string[] };
  try {
    result = JSON.parse(stdout) as typeof result;
  } catch (e) {
    post({ type: m.error, nodeId, message: `could not parse cmd/pseudo output: ${toErrorMessage(e)}` });
    return;
  }

  // Build one WorkspaceEdit spanning node.go + topology.json so a single Cmd-Z reverts both.
  let topologyParsed: unknown;
  try { topologyParsed = JSON.parse(document.getText()); } catch (e) {
    post({ type: m.error, nodeId, message: `could not parse topology: ${toErrorMessage(e)}` });
    return;
  }
  const topo = topologyParsed as {
    nodes?: { id: string; data?: Record<string, unknown> }[];
    edges?: { id: string; source: string; sourceHandle?: string; target: string; targetHandle?: string }[];
  };

  // (a) patch node data (no-op for readgate but keep parity)
  // (b) re-point ToChainInhibitor output edge
  if (Array.isArray(topo.edges)) {
    for (const edge of topo.edges) {
      if (edge.source === nodeId && edge.sourceHandle === READGATE_OUT_HANDLE) {
        edge.target = result.outNeighbor;
      }
    }
    // (c) prune edges whose targetHandle is in removedPorts
    topo.edges = topo.edges.filter(
      (e) => !(e.target === nodeId && result.removedPorts.includes(e.targetHandle ?? "")),
    );
  }
  const updatedTopoText = JSON.stringify(topologyParsed, null, 2);

  // Open the node.go TextDocument so we can get its full range.
  const goUri = vscode.Uri.file(goFile);
  let goDoc: vscode.TextDocument;
  try {
    goDoc = await vscode.workspace.openTextDocument(goUri);
  } catch (e) {
    post({ type: m.error, nodeId, message: `could not open node.go: ${toErrorMessage(e)}` });
    return;
  }

  const edit = new vscode.WorkspaceEdit();
  // node.go replacement
  edit.replace(
    goUri,
    new vscode.Range(goDoc.positionAt(0), goDoc.positionAt(goDoc.getText().length)),
    result.go,
  );
  // topology.json replacement
  edit.replace(
    document.uri,
    new vscode.Range(document.positionAt(0), document.positionAt(document.getText().length)),
    updatedTopoText,
  );
  await vscode.workspace.applyEdit(edit);

  // Save both documents.
  await document.save();
  await goDoc.save();

  post({ type: m.saveResult, nodeId });

  if (runner.isRunning()) {
    await runner.stopAndAwait();
    runner.run();
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

function findInputOutNeighbor(docText: string, nodeId: string): string | undefined {
  let parsed: unknown;
  try { parsed = JSON.parse(docText); } catch { return undefined; }
  const edges = (parsed as { edges?: unknown[] }).edges;
  if (!Array.isArray(edges)) return undefined;
  const edge = edges.find((e: unknown) => (e as { source?: string }).source === nodeId);
  if (!edge) return undefined;
  return (edge as { target?: string }).target;
}

async function handlePseudoRender(
  nodeId: string,
  document: vscode.TextDocument,
  post: (msg: HostToWebviewMsg) => Thenable<boolean>,
): Promise<void> {
  const m = pseudoMsgTypes("pseudo");
  const repoRoot = workspaceRoot();
  if (!repoRoot) {
    post({ type: m.error, nodeId, message: "no workspace folder" });
    return;
  }
  const specEntry = findNodeSpec(document.getText(), nodeId);
  if (!specEntry) {
    post({ type: m.error, nodeId, message: `node ${nodeId} not found in topology` });
    return;
  }
  const outNeighbor = findInputOutNeighbor(document.getText(), nodeId);
  if (!outNeighbor) {
    post({ type: m.error, nodeId, message: `Input node ${nodeId} has no output edge` });
    return;
  }
  const goFile = path.join(repoRoot, "nodes", "Input", "node.go");
  const { stdout, stderr, code } = await spawnGoRun(repoRoot, [
    "input", "render",
    "--go-file", goFile,
    "--out-neighbor", outNeighbor,
    "--spec-json", JSON.stringify(specEntry),
  ]);
  if (code !== 0) {
    let msg = stderr.trim();
    try { msg = (JSON.parse(msg) as { error?: string }).error ?? msg; } catch { /* use raw */ }
    post({ type: m.error, nodeId, message: msg });
    return;
  }
  post({ type: m.renderResult, nodeId, pseudo: stdout.trimEnd() });
}

async function handlePseudoSave(
  nodeId: string,
  pseudoText: string,
  document: vscode.TextDocument,
  runner: BuildAndRunRunner,
  post: (msg: HostToWebviewMsg) => Thenable<boolean>,
): Promise<void> {
  const m = pseudoMsgTypes("pseudo");
  const repoRoot = workspaceRoot();
  if (!repoRoot) {
    post({ type: m.error, nodeId, message: "no workspace folder" });
    return;
  }
  const specEntry = findNodeSpec(document.getText(), nodeId);
  if (!specEntry) {
    post({ type: m.error, nodeId, message: `node ${nodeId} not found in topology` });
    return;
  }
  const outNeighbor = findInputOutNeighbor(document.getText(), nodeId);
  if (!outNeighbor) {
    post({ type: m.error, nodeId, message: `Input node ${nodeId} has no output edge` });
    return;
  }
  const goFile = path.join(repoRoot, "nodes", "Input", "node.go");
  const { stdout, stderr, code } = await spawnGoRun(repoRoot, [
    "input", "save",
    "--go-file", goFile,
    "--out-neighbor", outNeighbor,
    "--spec-json", JSON.stringify(specEntry),
    "--pseudo", pseudoText,
  ]);
  if (code !== 0) {
    const raw = stderr.trim();
    let msg = raw;
    let suggestion = "";
    try {
      const parsed = JSON.parse(raw) as { error?: string; suggestion?: string };
      msg = parsed.error ?? raw;
      suggestion = parsed.suggestion ?? "";
    } catch { /* use raw */ }
    post({ type: m.error, nodeId, message: msg, suggestion });
    return;
  }
  let result: { go: string; spec: Record<string, unknown> };
  try {
    result = JSON.parse(stdout) as typeof result;
  } catch (e) {
    post({ type: m.error, nodeId, message: `could not parse cmd/pseudo output: ${toErrorMessage(e)}` });
    return;
  }

  // Write new Go source.
  const goUri = vscode.Uri.file(goFile);
  await vscode.workspace.fs.writeFile(goUri, Buffer.from(result.go, "utf8"));

  // Patch the node's data in topology.json and save.
  let topologyParsed: unknown;
  try { topologyParsed = JSON.parse(document.getText()); } catch (e) {
    post({ type: m.error, nodeId, message: `could not parse topology: ${toErrorMessage(e)}` });
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
  post({ type: m.saveResult, nodeId });

  // If a substrate run is active, stop it and restart so the new
  // topology.json + nodes/Input/node.go are picked up.
  if (runner.isRunning()) {
    await runner.stopAndAwait();
    runner.run();
  }
}
