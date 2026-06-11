import * as fs from "fs";
import * as path from "path";
import * as vscode from "vscode";
import { BuildAndRunRunner } from "./runCommand";
import type { HostToWebviewMsg } from "./messages";
import { buildWebviewHtml } from "./extension/html";
import { handleMessage } from "./extension/handle-message";

export function activate(context: vscode.ExtensionContext) {
  context.subscriptions.push(
    vscode.window.registerCustomEditorProvider(
      "topology.editor",
      new TopologyEditorProvider(context),
      { webviewOptions: { retainContextWhenHidden: true } },
    ),
  );
}

class TopologyEditorProvider implements vscode.CustomTextEditorProvider {
  constructor(private readonly context: vscode.ExtensionContext) {}

  async resolveCustomTextEditor(
    document: vscode.TextDocument,
    panel: vscode.WebviewPanel,
  ): Promise<void> {
    const outDir = vscode.Uri.file(path.join(this.context.extensionPath, "out"));
    panel.webview.options = { enableScripts: true, localResourceRoots: [outDir] };
    panel.webview.html = buildWebviewHtml(panel.webview, this.context.extensionPath);

    const post = (msg: HostToWebviewMsg) => panel.webview.postMessage(msg);
    const runner = new BuildAndRunRunner(
      (status) => post({ type: "run-status", ...status }),
      (event) => post({ type: "trace-event", event }),
    );

    // Suppress the `onDidChangeTextDocument` fire we trigger ourselves
    // by tracking the document version we last applied. Text-equality
    // breaks on no-op resaves (the same text fires a change event
    // whose version bumps); version comparison handles those correctly.
    let lastAppliedVersion = document.version;
    const scenePath = path.join(path.dirname(document.uri.fsPath), "topology.scene.json");
    const readSceneText = (): string | undefined => {
      try { return fs.readFileSync(scenePath, "utf8"); } catch { return undefined; }
    };
    const send = (): Thenable<boolean> => {
      const sceneText = readSceneText();
      const msg: HostToWebviewMsg = sceneText !== undefined
        ? { type: "load", text: document.getText(), sceneText }
        : { type: "load", text: document.getText() };
      return post(msg);
    };

    let restartTimer: ReturnType<typeof setTimeout> | undefined;
    const docSub = vscode.workspace.onDidChangeTextDocument((e) => {
      if (e.document.uri.toString() !== document.uri.toString()) return;
      if (e.document.version <= lastAppliedVersion) return;
      send();
      if (runner.isRunning()) {
        if (restartTimer) clearTimeout(restartTimer);
        restartTimer = setTimeout(() => { void (async () => { await runner.stopAndAwait(); runner.run(); })(); }, 300);
      }
    });
    const viewStateSub = panel.onDidChangeViewState(() => {
      if (!panel.visible) post({ type: "flush" });
    });

    // Hot-reload of the webview bundle (dev-loop). Gated on extension
    // mode rather than relying on the silent quirk that absolute-path
    // GlobPatterns never match for installed users.
    // RelativePattern rooted at the extension's `out` dir — absolute
    // string globs silently fail to match, which is why the watcher
    // appeared dead in dev.
    const bundleWatcher =
      this.context.extensionMode === vscode.ExtensionMode.Development
        ? vscode.workspace.createFileSystemWatcher(
            new vscode.RelativePattern(
              vscode.Uri.file(path.join(this.context.extensionPath, "out")),
              "webview.js",
            ),
          )
        : undefined;
    if (bundleWatcher) {
      console.log("[topology] bundleWatcher armed for", path.join(this.context.extensionPath, "out", "webview.js"));
      // Re-render the HTML in place. The `?v=<mtime>` cache-buster
      // baked into buildWebviewHtml's script/style URIs forces the
      // webview to fetch the fresh bundle instead of serving the
      // cached one. Debounced to absorb esbuild's multi-write builds.
      let pending: NodeJS.Timeout | undefined;
      const reload = (kind: string) => () => {
        console.log("[topology] bundleWatcher fired:", kind);
        if (pending) clearTimeout(pending);
        pending = setTimeout(() => {
          console.log("[topology] hot-reload: re-rendering webview.html");
          panel.webview.html = buildWebviewHtml(panel.webview, this.context.extensionPath);
        }, 150);
      };
      bundleWatcher.onDidChange(reload("change"));
      bundleWatcher.onDidCreate(reload("create"));
    } else {
      console.log("[topology] bundleWatcher NOT armed — extensionMode:", this.context.extensionMode);
    }

    this.context.subscriptions.push(docSub, viewStateSub, runner);
    if (bundleWatcher) this.context.subscriptions.push(bundleWatcher);

    panel.onDidDispose(() => {
      if (restartTimer) clearTimeout(restartTimer);
      docSub.dispose();
      bundleWatcher?.dispose();
      viewStateSub.dispose();
      runner.dispose();
    });

    panel.webview.onDidReceiveMessage((raw) =>
      handleMessage(raw, {
        document,
        runner,
        post,
        send,
        setLastAppliedVersion: (v) => { lastAppliedVersion = v; },
      }),
    );
  }
}
