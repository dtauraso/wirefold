// Append-mode writer for webview log entries. One JSON line per call,
// routed to .probe/ts.jsonl (normal) or .probe/ts-errors.jsonl (error
// labels: window-error, unhandled-rejection, render-error) in the
// document's workspace folder. External readers tail these files to
// observe the webview without DevTools.
//
// Appends serialize through a single promise chain per target file —
// concurrent bursts (boundary catch + window error firing for the same
// crash) would otherwise race on the read-then-write.

import * as fs from "fs/promises";
import * as path from "path";
import * as vscode from "vscode";

const ERROR_LABELS = new Set(["window-error", "unhandled-rejection", "render-error"]);

let pendingTs: Promise<void> = Promise.resolve();
let pendingTsErrors: Promise<void> = Promise.resolve();

export async function appendWebviewLog(
  entry: string,
  documentUri: vscode.Uri,
): Promise<void> {
  let parsed: { label?: string } | undefined;
  try { parsed = JSON.parse(entry); } catch { /* malformed — route to ts.jsonl */ }
  const isError = parsed?.label !== undefined && ERROR_LABELS.has(parsed.label);
  if (isError) {
    pendingTsErrors = pendingTsErrors.then(() => doAppend(entry, documentUri, "ts-errors.jsonl"));
    return pendingTsErrors;
  } else {
    pendingTs = pendingTs.then(() => doAppend(entry, documentUri, "ts.jsonl"));
    return pendingTs;
  }
}

async function doAppend(entry: string, documentUri: vscode.Uri, filename: string): Promise<void> {
  const folder = vscode.workspace.getWorkspaceFolder(documentUri);
  const baseDir = folder ? folder.uri.fsPath : path.dirname(documentUri.fsPath);
  const dir = path.join(baseDir, ".probe");
  const file = path.join(dir, filename);
  try {
    await fs.mkdir(dir, { recursive: true });
    await fs.appendFile(file, entry + "\n", "utf8");
  } catch (err) {
    console.warn("topology editor: webview-log append failed", err);
  }
}
