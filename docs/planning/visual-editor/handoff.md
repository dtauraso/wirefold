# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-27 — feature-audit re-verified and extended; no task in flight; on main)

- **Active branch:** `main`. No task in flight. `task/architecture-audit` was merged via merge commit `371d206a` and deleted local + remote. Working tree: only `topology.json` is modified (intentional node-drag positions — do NOT stage or discard).

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
- Deep-dive on saved views: NOT abandoned scaffolding — the panel was fully built (`20024759`, May 2) then deliberately removed (`d05d2376`, May 18, "remove saved-views panel UI") with an explicit note that dimming infra + SavedView state parsing were left intentionally. What remains and is LIVE/tested: SavedView type + parse/serialize + rename-remap; the dim mechanism it drove (`dimmed.ts` setDimmedImperative/getDimmed → specToFlow data.dimmed → .dim CSS, exercised by e2e `compare-fold-and-view.spec.ts` via window.__wirefold_test.applyDim). Stale comments at `main.tsx:57` and `webview.css:168` still reference the removed "views panel". Open disposition: was infra retention staging for a rebuild (~200 lines, resurrect from d05d2376) or should it be torn down (~150 lines, but drops the live dim mechanism + its e2e test)?

### KNOWN ISSUES (candidate next work)

1. **Drag-to-wire edge creation is NON-FUNCTIONAL** (top open item). Dragging node A onto node B does not create an edge. Confirm with a runtime breadcrumb before fixing; logic now lives in `three/edge-creation.ts` (`buildEdge`) and the pointer-up branch in `three/interaction-controls.ts`. NOTE: createEdge/store call sites moved during the audit — re-grep before assuming line numbers.
2. Node-to-node wiring fails for port-incompatible node kinds (same `buildEdge` auto-pick path).
3. **Pre-existing test failures (predate audit, unrelated):** TS — `parseSpec.test.ts` (2: legacy `timing.steps` not dropped; legend bad-kind not rejected), `diff-core.test.ts` (cascades from parseSpec fixture), `fold.test.ts` ("expanded fold emits a frame"). Go — `Trace.TestMarshalEventMatchesFixture`. (The two contract failures — topology-edge-handles, trace-event-fields — were FIXED this session.)
4. **Junction-click ambiguity:** overlapping edge pick-tubes near a node junction can mis-pick; click mid-span.
5. Dead-code orphans (feature-audit §3d) need disposition decisions — saved views (rebuild vs teardown, see above), diffSpecs (surface diff view vs drop), valueLabel (render vs strip from both layers), fold mutators (wire vs delete).

### Key files

- `tools/topology-vscode/src/webview/three/ThreeView.tsx` — orchestrator; render in `scene-content.tsx`, interaction in `interaction-controls.ts`, camera widgets in `camera-ui.tsx`, math in `geometry-helpers.ts`.
- `tools/topology-vscode/src/webview/three/store.ts` — thin Zustand store; fade in `fade-actions.ts`, edge creation in `edge-creation.ts`.
- `tools/topology-vscode/src/webview/three/fade.ts` — `computeFade` fixpoint (render-mask only).
- `tools/topology-vscode/src/schema/node-defs.ts` — generated node defs (now in spec layer); `src/schema/parse-spec.ts` — `requiredInputDiagnostics` (editor-diagnostic only).
- `nodes/Wiring/paced_wire.go` — `faded` flag + `SetFaded` + `Send` gate. `nodes/input/node.go` — Input node (also serves bootstrap role).
- `tools/topology-vscode/src/webview/state/viewer/types.ts` — SavedView type + parse/serialize + rename-remap (live, tested via e2e).
- `tools/topology-vscode/src/webview/state/dimmed.ts` — live dim mechanism (`setDimmedImperative`/`getDimmed`); drives `data.dimmed` → `.dim` CSS; exercised by `compare-fold-and-view.spec.ts`.
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
