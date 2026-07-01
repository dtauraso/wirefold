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
  // React Flow positions every node via inline `style="transform: ..."`
  // attributes, which `style-src` governs when `style-src-attr` is unset.
  // The bundled stylesheet is still served from cspSource; 'unsafe-inline'
  // is the minimal additional grant needed for RF to lay nodes out.
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
  <link rel="stylesheet" href="${styleUri}" />
</head>
<body>
  <div class="toolbar">
    <span id="status" class="clean">saved</span>
    <span id="run-mount"></span>
  </div>
  <div id="app"></div>
  <script nonce="${nonce}" src="${scriptUri}"></script>
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
