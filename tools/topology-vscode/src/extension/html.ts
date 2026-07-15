// Webview HTML template + CSP + nonce generation. Kept separate from
// the editor provider so the provider's wiring stays readable.

import * as crypto from "crypto";
import * as fs from "fs";
import * as path from "path";
import * as vscode from "vscode";

export function buildWebviewHtml(
  webview: vscode.Webview,
  extensionPath: string,
): string {
  const scriptPath = path.join(extensionPath, "out", "webview.js");
  const stylePath = path.join(extensionPath, "out", "webview.css");
  // Cache-buster: webview resource URIs are cached aggressively, so
  // Reload Window re-renders this HTML but the webview still fetches
  // the previous bundle. Stamping the mtime forces a fresh fetch
  // whenever the bundle on disk changes.
  const scriptUri = webview
    .asWebviewUri(vscode.Uri.file(scriptPath))
    .with({ query: `v=${mtimeMs(scriptPath)}` });
  const styleUri = webview
    .asWebviewUri(vscode.Uri.file(stylePath))
    .with({ query: `v=${mtimeMs(stylePath)}` });
  const nonce = randomNonce();
  // NOTE — the 'unsafe-inline' style-src grant below is UNJUSTIFIED as of the React Flow
  // erase. Its original rationale was that RF positioned every node via an inline
  // `style="transform: ..."` ATTRIBUTE, which `style-src` governs when `style-src-attr` is
  // unset. RF is gone; the renderer is a three.js canvas. React's `style={{...}}` prop
  // (16 sites) assigns CSSOM properties rather than emitting a style attribute, and CSP
  // does not govern CSSOM writes — so this grant is likely removable.
  //
  // NOT removed here because that needs a live editor check, not a grep: a dropped grant
  // that breaks rendering fails at runtime, and stop-checks cannot see it. Try removing
  // 'unsafe-inline', reload the webview, confirm the scene still draws.
  const csp = [
    `default-src 'none'`,
    `img-src ${webview.cspSource} data:`,
    `style-src ${webview.cspSource} 'unsafe-inline'`,
    `script-src 'nonce-${nonce}'`,
    `font-src ${webview.cspSource}`,
  ].join("; ");

  return `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta http-equiv="Content-Security-Policy" content="${csp}" />
  <title>Topology Editor</title>
  <link rel="stylesheet" href="${styleUri.toString()}" />
</head>
<body>
  <div class="toolbar">
    <span id="status" class="clean">saved</span>
    <span id="run-mount"></span>
  </div>
  <div id="rule-eq-mount"></div>
  <div id="app"></div>
  <script nonce="${nonce}" src="${scriptUri.toString()}"></script>
</body>
</html>`;
}

function randomNonce(): string {
  return crypto.randomBytes(24).toString("base64");
}

function mtimeMs(p: string): number {
  try {
    return Math.floor(fs.statSync(p).mtimeMs);
  } catch {
    return 0;
  }
}
