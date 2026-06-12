import * as fs from "fs";
import * as path from "path";
import * as vscode from "vscode";
import { BuildAndRunRunner } from "./runCommand";
import type { HostToWebviewMsg } from "./messages";
import { buildWebviewHtml } from "./extension/html";
import { handleMessage } from "./extension/handle-message";

export function activate(context: vscode.ExtensionContext) {
  context.subscriptions.push(
    vscode.commands.registerCommand("topology.openEditor", (uri?: vscode.Uri) => {
      openTopologyEditor(context, uri);
    }),
  );
}

function openTopologyEditor(context: vscode.ExtensionContext, folderUri?: vscode.Uri): void {
  // Resolve topology folder path. Command can be invoked from explorer context
  // menu (folderUri is the topology/ dir) or command palette (no uri).
  let topologyPath: string | undefined;
  if (folderUri) {
    topologyPath = folderUri.fsPath;
  } else {
    // Fallback: find topology/ dir in workspace root
    const folder = vscode.workspace.workspaceFolders?.[0];
    if (folder) {
      const candidate = path.join(folder.uri.fsPath, "topology");
      if (fs.existsSync(candidate)) topologyPath = candidate;
    }
  }

  const panel = vscode.window.createWebviewPanel(
    "topology.editor",
    "Topology Editor",
    vscode.ViewColumn.One,
    {
      enableScripts: true,
      retainContextWhenHidden: true,
      localResourceRoots: [vscode.Uri.file(path.join(context.extensionPath, "out"))],
    },
  );
  panel.webview.options = {
    enableScripts: true,
    localResourceRoots: [vscode.Uri.file(path.join(context.extensionPath, "out"))],
  };
  panel.webview.html = buildWebviewHtml(panel.webview, context.extensionPath);

  const post = (msg: HostToWebviewMsg) => panel.webview.postMessage(msg);
  const runner = new BuildAndRunRunner(
    (status) => post({ type: "run-status", ...status }),
    (event) => post({ type: "trace-event", event }),
    (spec) => {
      // Go emitted the spec on startup — send it to the webview as a load message.
      post({ type: "load", text: JSON.stringify(spec) });
    },
  );

  const viewStateSub = panel.onDidChangeViewState(() => {
    if (!panel.visible) post({ type: "flush" });
  });

  // Hot-reload of the webview bundle (dev-loop).
  const bundleWatcher =
    context.extensionMode === vscode.ExtensionMode.Development
      ? vscode.workspace.createFileSystemWatcher(
          new vscode.RelativePattern(
            vscode.Uri.file(path.join(context.extensionPath, "out")),
            "webview.js",
          ),
        )
      : undefined;
  if (bundleWatcher) {
    console.log("[topology] bundleWatcher armed for", path.join(context.extensionPath, "out", "webview.js"));
    let pending: NodeJS.Timeout | undefined;
    const reload = (kind: string) => () => {
      console.log("[topology] bundleWatcher fired:", kind);
      if (pending) clearTimeout(pending);
      pending = setTimeout(() => {
        console.log("[topology] hot-reload: re-rendering webview.html");
        panel.webview.html = buildWebviewHtml(panel.webview, context.extensionPath);
      }, 150);
    };
    bundleWatcher.onDidChange(reload("change"));
    bundleWatcher.onDidCreate(reload("create"));
  } else {
    console.log("[topology] bundleWatcher NOT armed — extensionMode:", context.extensionMode);
  }

  context.subscriptions.push(viewStateSub, runner);
  if (bundleWatcher) context.subscriptions.push(bundleWatcher);

  panel.onDidDispose(() => {
    bundleWatcher?.dispose();
    viewStateSub.dispose();
    runner.dispose();
  });

  panel.webview.onDidReceiveMessage((raw) =>
    handleMessage(raw, {
      runner,
      post,
      send: () => Promise.resolve(true), // no-op: Go sends spec on startup
    }),
  );

  // Spawn Go immediately (halted); it emits spec on startup which triggers load.
  runner.run(topologyPath);
}
