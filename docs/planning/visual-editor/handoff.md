# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-27 — saved-views fully removed on task/remove-saved-views; feature-audit updated)

- **Active branch:** `task/remove-saved-views`. Do NOT merge. Working tree: only `topology.json` is modified (intentional node-drag positions — do NOT stage or discard).

### What's on main (this session) — architecture + organization audit, all 13 findings resolved

- **ThreeView.tsx split** 1489→264 lines (orchestrator). New modules under `src/webview/three/`: `geometry-helpers.ts` (pure math), `scene-content.tsx` (render components), `interaction-controls.ts` (pointer/arcball/drag state machine), `camera-ui.tsx` (RollSlider/DollyButtons/PanPad).
- **store.ts split** 381→187 lines: fade logic → `three/fade-actions.ts` (`computeToggleFade`/`applyFade`/`reconcileFadeOrder`); edge creation → `three/edge-creation.ts` (`buildEdge`).
- **Layer-dependency fix:** generated `node-defs.ts` relocated from `src/webview/schema/` to `src/schema/` (spec layer). `src/webview/schema/` and its `registry.ts` shim are gone. So is `src/webview/rf/` (animation-fields.ts → `three/`).
- **Dead code removed:** `DimmedCtx`/`useDimmedCtx`/`registerDimmedSetter` (dimmed.ts), `PulseCtx`/`usePulseCtx` (pulse-state.ts).
- **edgeSeeds removed entirely** (TS fossil). Ring startup deadlock is broken by a dedicated **bootstrap Input node** (kind `Input`, `data.init=[seed]`, `repeat=false`) wired by a real edge into the receiving port; single-fires the seed at tick-0. (Memory `feedback_edge_seed_required_for_rings` corrected to match.)
- **Guards added:** `tools/check-generated.sh` (fails if generated TS files are stale; wired into `scripts/stop-checks.sh`); reflection test guarding readgate port-name constants; comments pinning `requiredInputDiagnostics` and `computeFade` as editor/render-only; `pseudoTable` documented as compile-time-exhaustive over `PseudoKind`.

### Feature-audit re-verification + dead-code sweep (2026-05-27)

- Re-verified all open feature-audit items against post-architecture-audit code (`eec03390`). Corrected drifts: undo/redo is NOT half-wired — it does not exist at all (no `state/history.ts`, no `pushSnapshot`); `folds.ts` → `state/folds-state.ts` (folds DO render as RF "note" nodes via buildFoldNodes, just no 3D mesh); sublabel inline edit is PARTIAL (`beginEditSublabel` exists in inline-edit.ts, no 3D trigger); §3a "proof of prior existence" files are deleted from tree (git-history-only now). Scorecard now: 26 working / 9 restore-parity + 4 half-wired + 1 not-started / 1 never-specced / 3 accepted-for-build / 4 dead-code orphans.
- Corrected memory `project_edge_midpoint_offset_plumbing`: `midpointOffset` is a schema-only stub (wire-defs.ts), NOT wired end-to-end as previously recorded.
- Added feature-audit §3d "Dead-Code Orphans" (`d154e30d`): named/saved views, spec diff (diffSpecs, test-only), wire `valueLabel` (schema-only, TS+Go), fold mutators (toggleFoldCollapse/updateFoldPosition/setFolds, zero callers).
- **Saved-views REMOVED** (commit `45cee602`, branch `task/remove-saved-views`): `SavedView` type + parse/serialize + rename-remap, `state/dimmed.ts`, `data.dimmed` in specToFlow + NodeData, `.dim` CSS, `__wirefold_test.applyDim` hook, `parseViewerState.test.ts`, and saved-view / `.dim` assertions in `compare-fold-and-view.spec.ts` (folds + diff assertions kept). The dim mechanism only ever drove a dead 2D React Flow `.dim` path — never the live R3F 3D diagram. Build/tsc/17 unit tests clean. Feature-audit §3d scorecard updated from 4 to 3 dead-code orphans. Remaining orphans: diffSpecs (test-only), valueLabel (schema-only TS+Go), fold mutators (zero callers).

### KNOWN ISSUES (candidate next work)

1. **Drag-to-wire edge creation is NON-FUNCTIONAL** (top open item). Dragging node A onto node B does not create an edge. Confirm with a runtime breadcrumb before fixing; logic now lives in `three/edge-creation.ts` (`buildEdge`) and the pointer-up branch in `three/interaction-controls.ts`. NOTE: createEdge/store call sites moved during the audit — re-grep before assuming line numbers.
2. Node-to-node wiring fails for port-incompatible node kinds (same `buildEdge` auto-pick path).
3. **Pre-existing test failures (predate audit, unrelated):** TS — `parseSpec.test.ts` (2: legacy `timing.steps` not dropped; legend bad-kind not rejected), `diff-core.test.ts` (cascades from parseSpec fixture), `fold.test.ts` ("expanded fold emits a frame"). Go — `Trace.TestMarshalEventMatchesFixture`. (The two contract failures — topology-edge-handles, trace-event-fields — were FIXED this session.)
4. **Junction-click ambiguity:** overlapping edge pick-tubes near a node junction can mis-pick; click mid-span.
5. Dead-code orphans (feature-audit §3d) need disposition decisions — diffSpecs (surface diff view vs drop), valueLabel (render vs strip from both layers), fold mutators (wire vs delete). Saved views are fully removed (commit `45cee602`).

### Key files

- `tools/topology-vscode/src/webview/three/ThreeView.tsx` — orchestrator; render in `scene-content.tsx`, interaction in `interaction-controls.ts`, camera widgets in `camera-ui.tsx`, math in `geometry-helpers.ts`.
- `tools/topology-vscode/src/webview/three/store.ts` — thin Zustand store; fade in `fade-actions.ts`, edge creation in `edge-creation.ts`.
- `tools/topology-vscode/src/webview/three/fade.ts` — `computeFade` fixpoint (render-mask only).
- `tools/topology-vscode/src/schema/node-defs.ts` — generated node defs (now in spec layer); `src/schema/parse-spec.ts` — `requiredInputDiagnostics` (editor-diagnostic only).
- `nodes/Wiring/paced_wire.go` — `faded` flag + `SetFaded` + `Send` gate. `nodes/input/node.go` — Input node (also serves bootstrap role).
- `tools/topology-vscode/src/webview/state/ops/diff.ts` — `diffSpecs` (test-only dead code; no production caller).

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
