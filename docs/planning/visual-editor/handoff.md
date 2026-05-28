# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-27 — branch main; no task in flight)

- **Active branch:** `main`. No task in flight. H1 (3D camera persistence) and H3 (single-message load transport) were resolved on branch `task/collapse-load-transport` and merged to `main` via merge commit `8b1d02a4`; branch deleted local + remote. Both features verified working in the editor. Working tree: only `topology.json` is modified (intentional node-drag positions — do NOT stage or discard).

### Load-transport collapse + 3D camera persistence (this session, merged 8b1d02a4)

- **H3 — Single-message load transport** (commit `e859e072`): deleted `loadView`, `_lastSpec`, the `view-load-noop` guard, the `view-load` IPC message variant, and the host-side `sendView` call. Spec and viewer-state now arrive in a single `load` message; the on-disk representation (topology.json) was already merged before this session.
- **H1 — Camera3D persistence** (commit `a267829e`): `Camera3D` position + quaternion added to the viewer-state schema and parser (`parse-viewer-state.ts`). Camera is committed to viewer-state on every orbit/dolly/pan/roll gesture-end. On `CameraRefBridge` mount, stored camera3d is applied and auto-fit is skipped when a saved camera is present.
- **H1 timing follow-up** (commit `dbfe1ca9`): added `initialCamera3d` to `CameraRefBridge` effect deps and called `updateMatrixWorld` so an async-arriving `camera3d` (loaded after the bridge mounts) still applies correctly.

### What's on main (this session) — architecture + organization audit, all 13 findings resolved

- **ThreeView.tsx split** 1489→264 lines (orchestrator). New modules under `src/webview/three/`: `geometry-helpers.ts` (pure math), `scene-content.tsx` (render components), `interaction-controls.ts` (pointer/arcball/drag state machine), `camera-ui.tsx` (RollSlider/DollyButtons/PanPad).
- **store.ts split** 381→187 lines: fade logic → `three/fade-actions.ts` (`computeToggleFade`/`applyFade`/`reconcileFadeOrder`); edge creation → `three/edge-creation.ts` (`buildEdge`).
- **Layer-dependency fix:** generated `node-defs.ts` relocated from `src/webview/schema/` to `src/schema/` (spec layer). `src/webview/schema/` and its `registry.ts` shim are gone. So is `src/webview/rf/` (animation-fields.ts → `three/`).
- **Dead code removed:** `DimmedCtx`/`useDimmedCtx`/`registerDimmedSetter` (dimmed.ts), `PulseCtx`/`usePulseCtx` (pulse-state.ts).
- **edgeSeeds removed entirely** (TS fossil). Ring startup deadlock is broken by a dedicated **bootstrap Input node** (kind `Input`, `data.init=[seed]`, `repeat=false`) wired by a real edge into the receiving port; single-fires the seed at tick-0. (Memory `feedback_edge_seed_required_for_rings` corrected to match.)
- **Guards added:** `tools/check-generated.sh` (fails if generated TS files are stale; wired into `scripts/stop-checks.sh`); reflection test guarding readgate port-name constants; comments pinning `requiredInputDiagnostics` and `computeFade` as editor/render-only; `pseudoTable` documented as compile-time-exhaustive over `PseudoKind`.

### Feature-audit re-verification + dead-code sweep (2026-05-27)

- Re-verified all open feature-audit items against post-architecture-audit code (`eec03390`). Corrected drifts: undo/redo is NOT half-wired — it does not exist at all (no `state/history.ts`, no `pushSnapshot`); `folds.ts` → `state/folds-state.ts` (at that point folds rendered as RF "note" nodes via buildFoldNodes, no 3D mesh — subsequently the entire old fold feature was removed, commit `9d4091c5`); sublabel inline edit is PARTIAL (`beginEditSublabel` exists in inline-edit.ts, no 3D trigger); §3a "proof of prior existence" files are deleted from tree (git-history-only now). Scorecard now: 26 working / 9 restore-parity + 4 half-wired + 1 not-started / 1 never-specced / 3 accepted-for-build / 4 dead-code orphans (all 4 subsequently removed this session — see the sweep bullets below; orphan count now 0).
- Corrected memory `project_edge_midpoint_offset_plumbing`: `midpointOffset` is a schema-only stub (wire-defs.ts), NOT wired end-to-end as previously recorded.
- Added feature-audit §3d "Dead-Code Orphans" (`d154e30d`): named/saved views, spec diff (diffSpecs, test-only), wire `valueLabel` (schema-only, TS+Go), fold mutators (toggleFoldCollapse/updateFoldPosition/setFolds, zero callers).
- **Saved-views REMOVED** (merged to `main` ~`96bb4a60`, branch deleted): `SavedView` type + parse/serialize + rename-remap, `state/dimmed.ts`, `data.dimmed` in specToFlow + NodeData, `.dim` CSS, `__wirefold_test.applyDim` hook, `parseViewerState.test.ts`, and saved-view / `.dim` assertions in `compare-fold-and-view.spec.ts` (fold assertions in that file were kept at the time; subsequently all fold tests removed, commit `9d4091c5`). The dim mechanism only ever drove a dead 2D React Flow `.dim` path — never the live R3F 3D diagram. Build/tsc/17 unit tests clean. Feature-audit §3d scorecard updated from 4 to 3 dead-code orphans. Remaining orphans: diffSpecs (test-only), valueLabel (schema-only TS+Go), fold mutators (zero callers).
- **diffSpecs REMOVED** (`state/ops/diff.ts` + `test/diff-core.test.ts` deleted): had no production caller; test-only dead code. Feature-audit §3d orphan count now 2. Remaining orphans: valueLabel (schema-only TS+Go), fold mutators (zero callers).
- **valueLabel REMOVED** (commit `9cc63677`, branch `main`): `valueLabel` struct field + wire tag removed from `nodes/Wiring/loader.go`; `wire-defs.ts` regenerated via gen-node-defs. Orphan count now 1.
- **Fold mutators REMOVED** (commit `93b6412a`, branch `main`): `setFolds`, `toggleFoldCollapse`, `updateFoldPosition` deleted from `state/folds-state.ts`; `getFolds` retained. Orphan count now 0 — all §3d dead-code orphans cleared.
- **Old fold feature FULLY REMOVED** (commit `9d4091c5`, branch `main`): deleted `Fold` type, `ops/fold.ts`, `folds-state.ts`, `buildFoldNodes`, `collapsedFoldFor` edge rerouting, `foldId` on NodeData, viewer-state fold parse/serialize, and all fold tests/e2e. The old view-only collapse/frame fold never had a 3D form and was dead infrastructure. Feature-audit §3a half-wired count 4→3, §3b never-specced count 1→0. A redesigned fold (file-dive containment, fold-as-attribute, self-contained child diagram, breadcrumb navigation) is captured as a not-implemented proposal in feature-audit §3b.

### KNOWN ISSUES (candidate next work)

1. **Drag-to-wire edge creation is NON-FUNCTIONAL** (top open item). Dragging node A onto node B does not create an edge. Confirm with a runtime breadcrumb before fixing; logic now lives in `three/edge-creation.ts` (`buildEdge`) and the pointer-up branch in `three/interaction-controls.ts`. NOTE: createEdge/store call sites moved during the audit — re-grep before assuming line numbers.
2. Node-to-node wiring fails for port-incompatible node kinds (same `buildEdge` auto-pick path).
3. **Pre-existing test failures (predate audit, unrelated):** TS — `parseSpec.test.ts` (2: legacy `timing.steps` not dropped; legend bad-kind not rejected). Go — `Trace.TestMarshalEventMatchesFixture`. (`fold.test.ts` is gone — the entire old fold test suite was deleted with the fold removal, commit `9d4091c5`.) (The two contract failures — topology-edge-handles, trace-event-fields — were FIXED in a prior session.)
4. **Junction-click ambiguity:** overlapping edge pick-tubes near a node junction can mis-pick; click mid-span.
5. ~~Dead-code orphans (feature-audit §3d)~~ — all four orphans removed (saved views, diffSpecs, valueLabel, fold mutators). No orphans remain.
6. **Fold (show/hide expand-in-place) — redesigned proposal, not implemented.** The old view-only collapse fold was fully removed. The redesign (feature-audit §3b) records: fold-as-attribute (any node marked a fold; no separate Fold kind); show/hide toggle button reveals ONE level down, expanding inline in-place (NOT full-screen dive, NOT breadcrumb navigation); top child node connects to the fold node as the anchor; visibility and execution are COUPLED (folded = hidden + not running; unfold = visible + running); folds can nest. Open gate: spec-layer vs. viewer-state-layer association for the child diagram — user chose to leave proposal as-is, not resolve now.

### Key files

- `tools/topology-vscode/src/webview/three/ThreeView.tsx` — orchestrator; render in `scene-content.tsx`, interaction in `interaction-controls.ts`, camera widgets in `camera-ui.tsx`, math in `geometry-helpers.ts`.
- `tools/topology-vscode/src/webview/three/store.ts` — thin Zustand store; fade in `fade-actions.ts`, edge creation in `edge-creation.ts`.
- `tools/topology-vscode/src/webview/three/fade.ts` — `computeFade` fixpoint (render-mask only).
- `tools/topology-vscode/src/schema/node-defs.ts` — generated node defs (now in spec layer); `src/schema/parse-spec.ts` — `requiredInputDiagnostics` (editor-diagnostic only).
- `nodes/Wiring/paced_wire.go` — `faded` flag + `SetFaded` + `Send` gate. `nodes/input/node.go` — Input node (also serves bootstrap role).

### Substrate model contract (stable)

See [MODEL.md](../../../MODEL.md#slot-phase-lifecycle). Fade did not change the model: it is a start-gate on `Send`, no new `PacedWire` op, slot-phase/AND-gate/backpressure untouched. `pump.ts` stays render-only.

## Dev-loop

After TS edit: `npm run build` from `tools/topology-vscode/`.
After Go change: `go build ./...` from repo root, `go test ./nodes/Wiring/...`.
Fade unit tests: `cd tools/topology-vscode && npx vitest run test/fade.test.ts`.
To repro / inspect: clear `.probe/*.jsonl`, reload window in VS Code, Run once, inspect logs.

Check: `go test ./...`. All guard scripts run via the Stop hook (`scripts/stop-checks.sh`). Bash approval guard runs via PreToolUse.

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
