# topology-vscode — architecture map

One-screen orientation. Read this before grepping into the source tree.

## Two sides

```
extension host (Node)              webview (browser)
─────────────────────              ─────────────────
  src/extension.ts          ◄──►   src/webview/main.ts
  src/runnerStatus.ts              src/webview/render/...
  src/runCommand.ts                src/webview/<feature>.ts
  src/sidecar.ts                   src/webview/state.ts
  src/schema.ts (shared)
```

esbuild bundles each side separately ([esbuild.mjs](esbuild.mjs)).
Communication is `panel.webview.postMessage` ↔ `vscode.postMessage`.

## Message protocol (single source of truth)

Webview → extension: `ready`, `save`, `view-save`, `run`, `run-cancel`.
Extension → webview: `load`, `view-load`, `run-status`.
Wired in `extension.ts` `panel.webview.onDidReceiveMessage`. Webview side
in `src/webview/save.ts` (sender) and `src/webview/main.tsx` (handler).

## Extension side — what lives where

| File | Owns |
|---|---|
| `extension.ts` | `CustomTextEditorProvider`, webview HTML/CSP, `applyEdit`, message dispatch |
| `runCommand.ts` | "▶ run" button: spawns `go run .`, streams to output channel, cancellable |
| `sidecar.ts` | `topology.view.json` URI computation + read/write |
| `schema.ts` | Node-type registry + edge-kind colors (imported by webview too) |

## Webview side — feature files

Each `webview/<feature>.ts` owns one UI affordance. Most expose
`init…Panel()` + `refresh…Panel()`.

| File | Affordance |
|---|---|
| `main.ts` | Entry point, message handler, top-level orchestration |
| `state.ts` | Mutable shared state (`spec`, `view`, `viewerState`, SVG_NS) |
| `viewerState.ts` | Types for sidecar (`Camera`, `SavedView`, `Fold`, `Bookmark`) |
| `save.ts` | `vscode` API handle + sync detection + post helpers |
| `view.ts` | Camera (pan/zoom) → `viewBox` |
| `views.ts` | Saved-views panel (top-right) |
| `timeline.ts` | Bottom timeline (play/pause/scrub + bookmark markers) |
| `playback.ts` | Master playback clock animations register against |
| `rename.ts` | Double-click node-id in-place edit |
| `run.ts` | "▶ run" button + status pill |
| `defs.ts` | SVG `<defs>` (markers, gradients) |
| `geom.ts` | Coordinate / hit-test helpers |
| `src/webview/three/ThreeView.tsx` | R3F 3D canvas — sole view: node drag, edge tubes, pointer state machine |
| `src/webview/three/store.ts` | zustand store: nodes/edges/selection, `loadSpec`/`loadView` actions |
| `src/webview/rf/adapter/spec-to-flow.ts` | Spec → store node/edge model conversion |

## Spec vs viewer state (load-bearing distinction)

- **`topology.json`** — read directly by the runtime loader at startup; every field maps to live wiring. Edited through `save` messages. Owned by `extension.ts` + the document.
- **`topology.view.json`** — sidecar; camera/views/folds/bookmarks. Edited
  through `view-save` messages. Owned by `sidecar.ts` on the extension
  side, `viewerState.ts` types on the webview side.

If a field affects generated Go, it belongs in the spec. Otherwise the
sidecar.

## Editor substrate

The webview renders with React Three Fiber (R3F) — a 3D canvas. The runtime loader stays authoritative — the substrate change was about *how nodes/edges are rendered and interacted with*, not about who owns the spec.

The topology has genuine depth (inhibitor chain, rings, lateral-inhibition lattices). 2D React Flow was retired because it flattened real 3D structure into misleading edge crossings. R3F is the sole view; RF types are kept for their node/edge shapes but no RF component is instantiated.

## Build

`npm run build` → `out/extension.js` (Node CJS) + `out/webview.js` (browser
IIFE) + `out/webview.css`. Watch mode via `npm run watch`.
