---
branch: task/full-code-audit
---

# Repo Hygiene Audit — 2026-05-26

Branch: `editor-r3f`

---

## Summary

Five boundary guard scripts all pass. The `audit-channel-names.sh` script flags three generic channel-variable names in Go files (not channel *naming* violations — they are local variables, not channel declarations — possible false-positive in the script). The `audit-spec-view-hygiene.mjs` script crashes immediately because it looks for `cmd/topogen/main.go` which does not exist (only `cmd/pseudo/` exists). The `audit-doc-drift.mjs` script finds ~70 broken file references across docs and memory — mostly caused by the R3F cutover that deleted the RF component tree (rf/edges/, rf/app/, etc.). No TypeScript errors were detectable via file reads; `tsc --noEmit` could not run due to permission denial. The highest-risk finding is that `rf/history.ts`'s `pushSnapshot()` is called from `ThreeView.tsx` but `registerHistory()` is never called (no `Inner()` mounts anymore), so undo/redo is silently inert and `pushSnapshot()` is a no-op — confirmed by handoff but unguarded in code.

---

## 1. Guard Scripts — Pass/Fail

| Script | Result | Notes |
|---|---|---|
| `tools/check-substrate-vocabulary.sh` | **PASS** | `substrate-vocabulary: clean` |
| `tools/check-trace-kind-parity.sh` | **PASS** | `trace-kind-parity: clean` |
| `tools/check-no-ts-timers.sh` | **PASS** | `no-ts-timers: clean` |
| `tools/check-message-kind-parity.sh` | **PASS** | `message-kind-parity: clean` |
| `tools/check-slot-phase-boundary.sh` | **PASS** | `slot-phase-boundary: clean` |
| `scripts/audit-channel-names.sh` | **FAIL (3)** | See §1a |
| `scripts/audit-spec-view-hygiene.mjs` | **FAIL (crash)** | See §1b |
| `scripts/audit-doc-drift.mjs` | **FAIL (~70)** | See §3 |
| `scripts/stop-checks.sh` | **PASS (silent)** | Only `topology.json` is dirty; `.go`/`.ts` guards don't trigger |

### 1a. Channel-name violations (`audit-channel-names.sh`)

```
nodes/Wiring/builders.go: generic name 'ch' (encode endpoints per CLAUDE.md)  [×2]
nodes/Wiring/paced_wire.go: generic name 'done' (encode endpoints per CLAUDE.md)
```

- `nodes/Wiring/builders.go:69` — `func (pb *PortBindings) SetSingle(name string, ch chan int)` — `ch` is a parameter name, not a channel field declaration. Likely a false positive in the script regex.
- `nodes/Wiring/builders.go:73` — `func (pb *PortBindings) AppendMulti(name string, ch chan int)` — same.
- `nodes/Wiring/paced_wire.go:41,79,142` — `done := pw.watchCtx(ctx)` — `done` is a local `chan struct{}` used as a cancellation stop-signal within `watchCtx`. The convention targets cross-node channel fields, not local lifetime guards; likely another false positive.

**Risk: LOW** — but the audit script itself generates noise that could mask real violations. The regex needs scope-narrowing (struct fields only).

### 1b. `audit-spec-view-hygiene.mjs` crash

```
Error: ENOENT: no such file or directory, open '.../cmd/topogen/main.go'
```

- `scripts/audit-spec-view-hygiene.mjs:12` reads `cmd/topogen/main.go` to derive the spec field allowlist from JSON struct tags. Only `cmd/pseudo/` exists; topogen was apparently never created or was removed.

**Risk: MEDIUM** — the script cannot run at all, so `topology.json` node/edge fields are never validated against what the Go loader actually consumes. If a field drifts in or out, no automation catches it.

---

## 2. Schema-Parser Parity

Checked `tools/topology-vscode/src/webview/state/viewer/types.ts` and `parse.ts`.

### Findings

**`bookmarks` in view allowlist but absent from `ViewerState` type and parser**

- `scripts/audit-spec-view-hygiene.mjs:16` — `viewAllowed` set includes `"bookmarks"`.
- `tools/topology-vscode/src/webview/state/viewer/types.ts` — `ViewerState` has no `bookmarks` field.
- `tools/topology-vscode/src/webview/state/viewer/parse.ts` — `parseViewerState` has no `bookmarks` branch.

If a `topology.view.json` ever contains `"bookmarks"`, the value is silently dropped on load. The allowlist and the parser are out of sync. Either the feature was planned and never implemented, or `bookmarks` was removed from the type but not from the allowlist.

**Risk: LOW** (bookmarks not currently written anywhere) but a latent load-blank vector per the `feedback_schema_parser_parity` rule.

**`bend` field described in handoff but not yet in types/parser**

- `docs/planning/visual-editor/handoff.md:77` specifies `bend?: { x, y, z }` on `EdgeView` for the next feature (edge midpoint drag).
- `tools/topology-vscode/src/webview/state/viewer/types.ts:48-50` — `EdgeView` has only `route?`.
- `tools/topology-vscode/src/webview/state/viewer/parse.ts:83-88` — `parseEdgeViews` has no `bend` branch.

This is expected (feature not yet implemented), but the schema/parser will need updating in the same commit per the parity rule.

**`parse.ts` camera parser assumes 2D `{x,y,zoom}` semantics**

- `parse.ts:20-28` — `parseCamera` validates `x`, `y`, `zoom` (or legacy `w/h`). For the R3F 3D editor, camera state will need 3D parameters (position, target, fov or similar). The current parser will silently discard any 3D camera shape, falling back to `undefined`.

**Risk: MEDIUM** — once 3D camera persistence is implemented, this parser must be extended in the same commit or reloads will lose camera position.

**All other `ViewerState` fields are parsed** — `camera`, `views`, `folds`, `lastSelectionIds`, `nodes` (including optional `z`, `sublabel`, `state`), `edges` (with `route`). No other persisted-but-unparsed fields found.

---

## 3. Verifying-Grep / Drift Hotspots

### 3a. Broken file references in docs (high-count, post-R3F-cutover)

`audit-doc-drift.mjs` found ~70 broken references. Highest-risk clusters:

**CLAUDE.md (project instructions) references deleted files:**
- `CLAUDE.md:6` — `tools/topology-vscode/src/webview/rf/edges/SubstrateEdge.tsx` — deleted in Slice 3. The "Wire props" section's landing rule now points to a nonexistent file; any AI session reading CLAUDE.md will be misdirected when adding edge wire props.
- **Risk: HIGH** — CLAUDE.md is the primary AI instruction file; stale file paths in it cause concrete wrong actions.

**MODEL.md references deleted file:**
- `MODEL.md` — `rf/edges/SubstrateEdge.tsx` — same deleted file.

**handoff.md references many deleted files** (all the RF component tree removed in Slice 3):
- `rf/app.tsx`, `rf/rf-imperative.ts` (listed as key files but app.tsx is gone), `rf/history.ts`, `rf/types.ts`, `src/webview/three/store.ts` (path format wrong — relative from docs, but the audit checks repo-root-relative), `tools/topology-vscode/src/webview/rf/pump.ts`, `nodes/inhibitrightgate/SPEC.md`, `docs/planning/visual-editor/rf-to-r3f-cutover.md`.
- **Note:** Some of these paths are intentionally relative to the doc's location; the drift script may be resolving them incorrectly. Verify manually for `rf/rf-imperative.ts` (file EXISTS at `tools/topology-vscode/src/webview/rf/rf-imperative.ts`) and `store.ts` (EXISTS at `tools/topology-vscode/src/webview/three/store.ts`).

**session-log.md references deleted files:**
- `rf/app.tsx`, `rf/AnimatedEdge.tsx`, `src/schema.ts` (5+ occurrences). These are historical log entries — stale is expected.

**memory/ references deleted/moved files:**
- `memory/feedback_schema_parser_parity.md` — references `tools/topology-vscode/src/schema/types.ts` and `src/schema/parse-nodes-edges.ts` (paths that don't exist).
- `memory/project_edge_midpoint_offset_plumbing.md` — references `rf/adapter/spec-to-flow.ts` (EXISTS), `rf/app/_use-edge-handlers.ts` (deleted), `rf/edges/SubstrateEdge.tsx` (deleted).
- `memory/feedback_enforce_required_inputs.md` — references `nodes/Wiring/validate.go` which EXISTS.
- `memory/feedback_hook_block_means_stop.md` — references `.claude/hooks/substrate-r-model-derive.sh` which does not exist.
- `memory/feedback_runner_errors_probe_first.md`, `memory/MEMORY.md` — reference `.probe/runner-errors-last.json` etc. — these are runtime-generated files, not committed; expected absent.

### 3b. Key string/key duplications across files

**Node kind names duplicated in `node-defs.ts` and Go `registry.go`**
- `tools/topology-vscode/src/webview/rf/nodes/node-defs.ts` defines `NODE_DEFS` with PascalCase kind keys.
- Go `nodes/*/node.go` + `nodes/Wiring/registry.go` register the same kinds.
- No automated cross-check script exists to verify the TS set matches the Go set. If a new kind is added in Go but not in `NODE_DEFS` (or vice versa), no guard catches it.
- **Risk: MEDIUM** — the parity is manual; it has drifted before (the CLAUDE.md primitive landing rule requires both in the same commit, but there's no enforcement).

**`"substrate"` edge type string**
- `tools/topology-vscode/src/webview/three/store.ts:144` — hardcoded `type: "substrate"` on new edges.
- `tools/topology-vscode/src/webview/rf/adapter/spec-to-flow.ts` — also produces `type: "substrate"` edges.
- No constant defined; duplicated string literal. Low risk currently (RF edge type registry is being retired) but worth noting.

### 3c. `rf/history.ts` — pushSnapshot is a no-op at runtime

- `tools/topology-vscode/src/webview/three/ThreeView.tsx:18,831` — imports and calls `pushSnapshot()`.
- `tools/topology-vscode/src/webview/inline-edit.ts:7,83` — same.
- `tools/topology-vscode/src/webview/rf/history.ts:39-46` — `pushSnapshot()` guards on `if (!_rf) return` where `_rf` is set only by `registerHistory(rf)`.
- `registerHistory` is never called anywhere outside `history.ts` itself (no `Inner()` component exists after Slice 2 removal).
- **Result:** every `pushSnapshot()` call silently no-ops. Undo/redo state is never populated. The handoff acknowledges this ("undo/redo wired-but-inert") but there is no code-level comment or guard at the call sites.
- **Risk: MEDIUM** — a developer looking at `ThreeView.tsx:831` would believe undo is being recorded. A `TODO` or early-return-with-warning at the `pushSnapshot` call sites would make the inert state explicit.

### 3d. `reactflow/dist/style.css` import with no RF components

- `tools/topology-vscode/src/webview/main.tsx:3` — `import "reactflow/dist/style.css"` — loads RF CSS into every webview despite no RF component being rendered.
- Harmless at runtime but a residue that bloats the bundle and signals incomplete cleanup.

---

## 4. Stale Handoff / Planning Claims

`docs/planning/visual-editor/handoff.md` — verified against actual code:

| Claim | Status |
|---|---|
| `three/ThreeView.tsx` — "whole (sole) 3D view" | **CORRECT** — file exists at `tools/topology-vscode/src/webview/three/ThreeView.tsx` |
| `three/store.ts` — "single zustand source of truth" | **CORRECT** — file exists |
| `main.tsx` — "renders only ThreeView" | **CORRECT** |
| `save.ts`, `pump.ts` — "read from store now" | **CORRECT** — store imports verified |
| `rf/rf-imperative.ts` — "LEGACY, only history.ts uses it" | **CORRECT** — `history.ts:11` is the only importer |
| `rf/app.tsx` — listed as Key File | **STALE** — deleted in Slice 3; does not exist |
| `rf/history.ts` — listed as Key File | **MISLEADING** — exists but all its runtime paths (`pushSnapshot`, `undo`, `redo`) are inert because `registerHistory` is never called |
| `docs/planning/visual-editor/rf-to-r3f-cutover.md` — listed as Key File | **EXISTS** — file present |
| `tools/pseudo/chaininhibitor.go`, `tools/pseudo/readgate.go` — deferred task files | **CORRECT** — exist under `tools/pseudo/` |
| `cmd/pseudo/main.go` — deferred task | **CORRECT** — exists |
| `nodes/inhibitrightgate/SPEC.md` — deferred task | **CORRECT** — exists |
| `tools/topology-vscode/src/handle-message.ts` — deferred task | **PATH WRONG** — actual path is `src/extension/handle-message.ts` |
| `tools/topology-vscode/src/webview/rf/PseudoPanel.tsx` — deferred task | **STALE** — deleted in Slice 3 (panels/ only has `RunButton.tsx`) |
| `scripts/stop-checks.sh` — "runs automatically via Stop hook" | **CORRECT** — wired at `.claude/settings.json:39` |

---

## 5. TypeScript — `tsc --noEmit`

**Could not run** — `npx tsc --noEmit` in `tools/topology-vscode/` was denied by the permission system during this audit session. The `stop-checks.sh` hook runs it automatically on TS file changes; the last known build state is green per the most recent commits. No type errors were found via static file reading (all imports resolved to existing files; no obvious type mismatches). The `store.ts` import of `parseViewerState` from `../state/viewer/types` (the types file re-exports the function) is correct.

---

## Risk Ranking

| # | Finding | Risk | File:line |
|---|---|---|---|
| 1 | `CLAUDE.md` references deleted `rf/edges/SubstrateEdge.tsx` — AI sessions will follow wrong instructions for wire prop additions | HIGH | `CLAUDE.md:6` |
| 2 | `audit-spec-view-hygiene.mjs` crashes — `topology.json` fields never validated against Go loader | MEDIUM | `scripts/audit-spec-view-hygiene.mjs:12` |
| 3 | `pushSnapshot()` silently no-ops everywhere (undo inert, no call-site warning) | MEDIUM | `ThreeView.tsx:831`, `inline-edit.ts:83`, `history.ts:39` |
| 4 | `parseCamera` in `parse.ts` will silently discard 3D camera shapes when that feature lands | MEDIUM | `parse.ts:20-28` |
| 5 | TS/Go node-kind parity has no enforcement script | MEDIUM | `rf/nodes/node-defs.ts`, `nodes/Wiring/registry.go` |
| 6 | `bookmarks` in view allowlist but absent from `ViewerState` type and parser | LOW | `audit-spec-view-hygiene.mjs:16`, `types.ts:52-59` |
| 7 | `handoff.md` lists `rf/app.tsx` and `PseudoPanel.tsx` as Key Files but both are deleted | LOW | `handoff.md:96,109` |
| 8 | `reactflow/dist/style.css` imported with no RF components rendered | LOW | `main.tsx:3` |
| 9 | `audit-channel-names.sh` false positives on parameter names / local vars | LOW | `builders.go:69,73`, `paced_wire.go:41` |
| 10 | `MODEL.md` references deleted `rf/edges/SubstrateEdge.tsx` | LOW | `MODEL.md` |
