# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-26 — On task/feature-audit; feature audit doc landed (e3c9d7e0). main has R3F sole editor (merged ccbff474).)

**Active branch:** `task/feature-audit`. Branched off main after editor-r3f was merged at ccbff474.

Branch-local doc: `docs/planning/visual-editor/feature-audit.md` (landed in commit e3c9d7e0, frontmatter `branch: task/feature-audit`). Will be stripped by `tools/strip-branch-local-docs.sh` before any merge unless relocated.

### Last completed — feature audit of the 3D R3F visual editor

Produced a planned-vs-implemented feature audit covering the full surface of the editor as of the R3F sole-editor state on main.

**Scorecard:**
- ~26 features implemented and working
- 5 partial / wired-but-inert
- 11 planned-not-built
- 10 deferred-by-design

**Key gaps found (planned, not built — mostly direct-manipulation editing affordances lost in the RF→R3F cutover):**
multi-select, node delete, edge delete, edge reconnect, node palette / add-node, sublabel inline edit, PseudoPanel-in-3D, port drag, edge-kind context menu, edge midpoint drag, grow-handle, Fold node Go primitive.

**Partial / inert:**
- **Undo-redo:** snapshot stack implemented (`history.ts`) and pushSnapshot exists, but only the edge-create path calls it. Node drags do not push snapshots — undo does not cover drags.
- **Interactive view-save:** `markViewSynced` is called on `loadView` (store.ts:76) but not after camera moves or node drags. Drag/camera positions do not persist across reloads without a manual Save.
- **Fit-view:** fit-on-load only; no keyboard shortcut (f / Shift-F) for manual re-fit.
- **Folds:** state module exists; no 3D mesh rendering.
- **Z-coordinate:** schema supports it; always 0 in practice.

**Verification correction (audit-correctness.md H1/H2 are now STALE):** those entries claim `setSpecMeta` and `markViewSynced` are "never called." Both claims no longer hold against current code — `setSpecMeta` IS called (store.ts:66) and `markViewSynced` IS called on loadView (store.ts:76). The audit-correctness.md doc should be annotated or removed before any merge.

### Open decisions / next

**(a) feature-audit.md fate:** decide whether to leave it branch-local (stripped on merge, audit is lost) or relocate it to `docs/planning/visual-editor/` with updated frontmatter so it survives the merge. The audit is reference material for future friction-driven work; relocation is likely worth it.

**(b) Backlog from the 11 gaps:** these are the candidate list for friction-driven new work. Highest-friction candidates (basic editing affordances that users will hit immediately): **node/edge delete** and **multi-select**. Those were present in the RF editor and are conspicuously absent in the 3D editor. Pick one as the next task branch when friction justifies it.

**(c) audit-correctness.md stale claims:** H1 and H2 are factually wrong against current code. Annotate with a correction note or drop the doc before merging the branch.

**(d) Interactive view-save gap:** `markViewSynced` is not called after camera or node-drag interactions, so positions are lost on reload without an explicit Save. This is a real residual gap worth a focused fix (small scope, clear contract).

**(e) topology.json working-tree modification:** node-drag view positions are modified but uncommitted intentionally. Do NOT stage or discard.

### Key files

- `tools/topology-vscode/src/webview/three/ThreeView.tsx` — the whole (sole) 3D view: node drag, edge tubes, pointer state machine.
- `tools/topology-vscode/src/webview/three/store.ts` — single zustand source of truth (nodes/edges/selection, load/save actions). `setSpecMeta` called at :66; `markViewSynced` called on loadView at :76.
- `tools/topology-vscode/src/webview/main.tsx` — renders only ThreeView; feeds store on load; hoisted run/save toolbar; posts `{ type: "ready" }` to unblock host load sequence.
- `tools/topology-vscode/src/webview/save.ts`, `tools/topology-vscode/src/webview/three/pump.ts` — read from the store.
- `tools/topology-vscode/src/webview/three/pulse-state.ts` — R3F pulse read-store (getPulseMap, setPulse).
- `tools/topology-vscode/src/webview/types.ts` — local `RFNode`/`RFEdge` type aliases (no reactflow import).
- `tools/topology-vscode/src/webview/state/adapter/{spec-to-flow,flow-to-spec}.ts` — pure adapters, RF-free.
- `tools/topology-vscode/src/webview/rf/` — two residual re-export/metadata files (`adapter.ts`, `animation-fields.ts`); folder name is a misnomer post-retirement.
- `tools/topology-vscode/src/webview/schema/` — node-defs.ts + registry.ts (relocated from rf/).
- `docs/planning/visual-editor/feature-audit.md` — branch-local audit doc (frontmatter `branch: task/feature-audit`).

### Substrate model contract (stable)

See [MODEL.md](../../../MODEL.md#slot-phase-lifecycle). Unchanged by the
3D move — going 3D is a medium change; the Go substrate,
slot-phase/AND-gate/backpressure model, and `pump.ts` firing logic stay
untouched.

## Dev-loop

After TS edit: `npm run build` from `tools/topology-vscode/`.
After Go change: `go build ./...` from repo root, `go test ./nodes/Wiring/...`.
After pseudo change (deferred branch): `go test ./tools/pseudo/...`.
To repro / inspect: clear `.probe/*.jsonl`, reload window in VS Code, Run once, inspect logs.

Check: `go test ./...`. All five guard scripts — the four boundary guards plus
`check-substrate-vocabulary` — run automatically via the Stop hook (`scripts/stop-checks.sh`).

## ALWAYS clause

At end of session, overwrite this file with a freshly-rendered prompt
tailored to the state you're leaving the branch in, and commit on the
active branch (main if no task is in flight). Do not rely on chat
history; the next AI may be a fresh model with no transcript. The
rendered handoff must itself contain this same ALWAYS clause so the
loop is self-perpetuating across sessions. Use
[continuation-prompt-template.md](continuation-prompt-template.md) as
the structural source of truth; update the template when an invariant
changes.
