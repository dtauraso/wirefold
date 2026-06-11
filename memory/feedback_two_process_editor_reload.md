---
name: feedback_two_process_editor_reload
description: VS Code webview vs extension host are separate processes; reopen-file reloads only the webview, Developer-Reload-Window reloads the extension host
metadata:
  type: feedback
---

The topology editor runs as TWO separate VS Code processes: the **webview** (`out/webview.js` — React/three render) and the **extension host** (`out/extension.js` — Go-process spawn, stdout trace parsing, the bridge). Reopening the topology file reloads ONLY the webview; the extension host keeps running its old code. To pick up extension-host changes you must run **Developer: Reload Window**. `npm run build` refreshes the on-disk bundles but does NOT reload the running host.

**Why:** A session was nearly lost chasing a "stale `flagged` bundle" and "Go not emitting geometry" when the real issue was the running extension host executing pre-rebuild code; reopen-file cleared the webview crash but left the stale host.

**How to apply:** When an editor change "doesn't take" after a rebuild, Reload Window before theorizing. For runtime truth prefer `.probe/*.jsonl` logs and `go run`/`go test` over gopls (which also goes stale). See [[feedback_webview_devtools_frame]], [[feedback_runtime_breadcrumbs_beat_static_analysis]].
