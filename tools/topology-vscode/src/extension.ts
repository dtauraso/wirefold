import * as fs from "fs";
import * as path from "path";
import * as vscode from "vscode";
import { BuildAndRunRunner } from "./runCommand";
import { buildBinary } from "./goBuild";
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

// Truncate probe logs on each editor open so each session's trace is clean (cross-session accumulation was misleading).
function resetProbeLogs(repoRoot: string): void {
  try {
    const probeDir = path.join(repoRoot, ".probe");
    fs.mkdirSync(probeDir, { recursive: true });
    for (const name of ["ts.jsonl", "ts-errors.jsonl", "go.jsonl", "go-errors.jsonl"]) {
      fs.writeFileSync(path.join(probeDir, name), "");
    }
  } catch {
    // Swallow: logging reset must never block opening the editor.
  }
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

  // Reset probe logs early: same workspace root the runner (.probe/go*.jsonl) and
  // appendWebviewLog (.probe/ts*.jsonl) write to, before any log can be appended.
  const probeRoot = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;
  if (probeRoot) resetProbeLogs(probeRoot);

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
  panel.webview.html = buildWebviewHtml(panel.webview, context.extensionPath);

  // Fire-and-forget host→webview send (bridge doctrine: no await, no Promise
  // chain — see check-no-await-on-bridge). `void` discards the postMessage
  // Thenable so this returns void and can be passed where VS Code expects a
  // void-returning callback, without floating the promise.
  const post = (msg: HostToWebviewMsg): void => {
    void panel.webview.postMessage(msg);
  };
  // Read the scene sidecar (topology/view/scene.json) fresh at load time so the
  // navigated camera (camera3d) is delivered to the webview as `sceneText`.
  // Without this the spec from Go carries only nodes/edges/view (diagram) and
  // viewerState.camera3d stays undefined on reload — hasRestoredCamera is then
  // false at first content render and CameraFitter's auto-fit clobbers the saved
  // pose. Re-read on each load (not cached) so a save written between reloads
  // restores correctly.
  const readSceneText = (): string | undefined => {
    if (!topologyPath) return undefined;
    try {
      return fs.readFileSync(path.join(topologyPath, "view", "scene.json"), "utf8");
    } catch {
      return undefined; // no sidecar yet → auto-fit frames the graph once (intended)
    }
  };
  let lastSpec: { nodes: unknown[]; edges: unknown[]; view?: unknown } | undefined;
  const runner = new BuildAndRunRunner(
    (status) => post({ type: "run-status", ...status }),
    (event) => post({ type: "trace-event", event }),
    (spec) => {
      // Go emitted the spec on startup — cache it and send it to the webview as a load message.
      lastSpec = spec;
      post({ type: "load", text: JSON.stringify(spec), sceneText: readSceneText() });
    },
    // fd3 buffer-snapshot frames: forward each to the webview verbatim. Without this
    // wiring the runner reads fd3 (handleFd3) but drops every frame, so the new-system
    // BufferScene (which polls getLatestSnapshot each frame) never receives node/edge/
    // camera/bead geometry and renders nothing. Fire-and-forget host→webview send.
    (snapshot) => post(snapshot),
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

  // Eager Go-binary watcher: rebuild the prebuilt binary the moment a .go file
  // is saved so launches stay instant (the lazy ensureBinaryBuilt in runner.run()
  // remains the safety net for missed events). Does NOT hot-restart a running sim
  // on .go change — it only keeps the binary fresh; the next start/restart picks
  // it up. (Hot-restart of a live sim on .go change is a possible future
  // enhancement, intentionally not implemented here.)
  const repoRoot = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;
  let goWatcher: vscode.FileSystemWatcher | undefined;
  if (repoRoot) {
    const binPath = path.join(repoRoot, ".wirefold-cache", "wirefold");
    const goErrorsFile = path.join(repoRoot, ".probe", "go-errors.jsonl");
    const goChannel = vscode.window.createOutputChannel("topology go-build");
    goWatcher = vscode.workspace.createFileSystemWatcher(
      new vscode.RelativePattern(repoRoot, "**/*.go"),
    );
    let pending: NodeJS.Timeout | undefined;
    const rebuild = () => {
      if (pending) clearTimeout(pending);
      pending = setTimeout(() => {
        const res = buildBinary(repoRoot, binPath);
        if (res.ok) {
          if (!res.busy) goChannel.appendLine("[go] rebuilt wirefold");
        } else {
          goChannel.appendLine(`[go] build error: ${res.error}`);
          try {
            fs.mkdirSync(path.dirname(goErrorsFile), { recursive: true });
            fs.appendFileSync(
              goErrorsFile,
              JSON.stringify({ ts_ms: Date.now(), src: "go", kind: "error", message: res.error }) + "\n",
              "utf8",
            );
          } catch { /* swallow */ }
        }
      }, 250);
    };
    goWatcher.onDidChange(rebuild);
    goWatcher.onDidCreate(rebuild);
    goWatcher.onDidDelete(rebuild);
    // goWatcher/goChannel track THIS panel's lifetime, so the panel is their single
    // disposal owner (onDidDispose below). Deliberately NOT pushed into
    // context.subscriptions — mirrors the bundleWatcher single-owner contract and
    // avoids a double-dispose across the two owners.
    panel.onDidDispose(() => goChannel.dispose());
  }

  context.subscriptions.push(viewStateSub, runner);
  // bundleWatcher tracks THIS panel's lifetime, so the panel is its single disposal
  // owner (onDidDispose below). It is deliberately NOT pushed into
  // context.subscriptions to avoid a muddled double-owner contract.

  panel.onDidDispose(() => {
    bundleWatcher?.dispose();
    goWatcher?.dispose();
    viewStateSub.dispose();
    runner.dispose();
  });

  panel.webview.onDidReceiveMessage((raw) => {
    // If the webview just mounted and we have a cached spec, replay it so the
    // diagram renders even when Go's one-shot startup emission beat the listener.
    if ((raw as Record<string, unknown>)?.type === "ready" && lastSpec !== undefined) {
      post({ type: "load", text: JSON.stringify(lastSpec), sceneText: readSceneText() });
    }
    const workspaceFolder = folderUri ? vscode.workspace.getWorkspaceFolder(folderUri) : undefined;
    // Final fallback is undefined (no real workspace) — appendWebviewLog skips the
    // write rather than misdirecting .probe/ logs to an arbitrary cwd.
    const logUri = workspaceFolder?.uri ?? folderUri ?? vscode.workspace.workspaceFolders?.[0]?.uri;
    void handleMessage(raw, { logUri, runner, post }).catch((err: unknown) => {
      console.error("topology: handleMessage failed", err);
    });
  });

  // Spawn Go immediately (halted); it emits spec on startup which triggers load.
  runner.run(topologyPath);
}
