# topology-vscode — architecture map

One-screen orientation. Read this before grepping into the source tree. The
full model (bead, wire, node goroutine, buffer, bridge) lives in
[../../MODEL.md](../../MODEL.md) and [../../CLAUDE.md](../../CLAUDE.md) —
this file is only the file-layout map for this package.

## Two sides

```
extension host (Node)                webview (browser)
─────────────────────                ─────────────────
  src/extension.ts            ◄──►   src/webview/main.tsx
  src/runCommand.ts                  src/webview/three/ThreeView.tsx
  src/extension/handle-message.ts    src/webview/three/buffer-scene.tsx
  src/extension/html.ts              src/webview/three/buffer-decode.ts
  src/goBuild.ts                     src/webview/snapshot-buffer.ts
  src/schema/* (shared)
```

esbuild bundles each side separately ([esbuild.mjs](esbuild.mjs)).
Communication is `panel.webview.postMessage` ↔ `vscode.postMessage`, wired in
`extension.ts` `panel.webview.onDidReceiveMessage`.

## Message protocol (single source of truth)

`src/messages.ts` is the shared discriminated-union source for both sides.
`WebviewToHostMsg` includes `ready` and the binary bridge envelope (a fully
encoded editor→Go record built via `src/schema/input-layout.ts` and written
FRAMED to Go's stdin by `runCommand.ts`); `HostToWebviewMsg` carries the
decoded content-buffer snapshot. Extension-side dispatch is
`src/extension/handle-message.ts`. Per CLAUDE.md, Go → TS is the binary
content buffer and nothing else; TS → Go is framed binary records
(addressed `edit` ops, or the bare `save` command) — see CLAUDE.md
for the full bridge-surface model, not duplicated here.

**Do not restate the kind list here.** The authority is
`INPUT_LAYOUT_FINGERPRINT` — one string encoding every kind byte, update kind,
attr, and overlay flag, defined in `nodes/Wiring/input_codec.go` and mirrored in
`src/schema/input-layout.ts`. `tools/check-input-layout-parity.sh` compares the
two, so drift fails a check instead of silently outliving a doc paragraph. Read the fingerprint to learn the
current surface; prose copied into this file cannot fail and so cannot be
trusted. (Removed kind bytes are preserved as GAPS in `input_codec.go` and never
renumbered.)

## Extension side — what lives where

| File | Owns |
|---|---|
| `extension.ts` | `topology.openEditor` command → `createWebviewPanel`; message dispatch |
| `src/extension/handle-message.ts` | Routes `WebviewToHostMsg` to the Go process / disk |
| `src/extension/html.ts` | Webview HTML shell + CSP |
| `runCommand.ts` | Spawns/streams the Go process; frames stdin records; decodes breadcrumbs |
| `goBuild.ts` | Compiles the Go binary; invoked automatically on `ready`, not by a button |
| `schema/*.ts` | Node-type registry (`node-defs.ts`), buffer layout, wire props — shared with the webview |

## Webview side

The webview is React Three Fiber (R3F) — a single 3D canvas. There are no
per-kind render components; `buffer-scene.tsx` draws every node/edge/bead
generically from the decoded content buffer, keyed off `NODE_DEFS`
(`src/schema/node-defs.ts`).

| File | Role |
|---|---|
| `src/webview/main.tsx` | Entry point, message handling |
| `src/webview/snapshot-buffer.ts` | Raw buffer receive/framing on the webview side |
| `src/webview/three/buffer-decode.ts` | Decodes the binary content buffer into a typed snapshot |
| `src/webview/three/buffer-scene.tsx` | Draws the whole scene generically from the decoded snapshot |
| `src/webview/three/ThreeView.tsx` | R3F `<Canvas>` root. Holds NO gesture state — raw pointer/wheel events forward verbatim to Go's FSM (`nodes/Wiring/gesture.go`) |
| `src/webview/three/raw-input.ts` | Raw pointer/wheel + raycast hit → binary `raw-input` record to Go |
| `src/webview/three/overlay-flags.ts` | Read-only reflection of Go-owned overlay-toggle state (`useSyncExternalStore`; no store) |
| `webview/log/*` | Crash listeners, error boundary, log posting to the extension host |

There is no JSON-trace render path, no `pump.ts`, and no zustand/Redux-style
store — the TS layer is render + forward only (guard:
`tools/check-no-webview-state.sh`).

## Spec vs viewer state

- **The `topology/` tree** — read directly by the Go loader (`nodes/Wiring/loader.go`,
  `loader_tree.go`) at startup; every field maps to live wiring. Edited through `edit`
  messages. The live form is a directory tree — `nodes/<id>/meta.json`, `data.json`,
  `inputs/`, `outputs/`, and `edges/*.json`. A monolithic single-file `topology.json`
  is still accepted as a legacy fallback, but is not what the editor opens.
- **`<tree-root>/view/scene.json`** — sidecar for camera/view state not affecting
  generated Go. Path computed by `sceneJSONPath` (`nodes/Wiring/scene_paths.go`).

If a field affects generated Go, it belongs in the spec. Otherwise the sidecar.

## Build

`npm run build` → `out/extension.js` (Node CJS) + `out/webview.js` (browser
IIFE) + `out/webview.css`. Watch mode via `npm run watch`.
