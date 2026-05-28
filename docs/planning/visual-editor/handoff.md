# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-28 — audit board status cleanup)

- **Active branch:** `main`. Local + origin/main in sync at `6a1b2c99`.
- Working tree: `topology.json` modified (pre-existing, untouched this session).
- Build/test gate last verified at the pulse-substrate-transport merge (2026-05-28). This session was docs-only.

### What this session did (commits a45ebcae, 6a1b2c99 on main)

1. **Introduced REWORK verification status** (orange badge) in `feature-audit/styles.css` for features that need to be redone rather than removed.
2. **Re-classified billboarded-node-labels and arcball-camera-controls** from NEEDS_REMOVAL → REWORK in `data.js` and per-feature HTML.
3. **Removed fold-containment from the audit site** (`features/fold-containment.html` deleted, entry removed from `data.js`). Old fold was already gone from code; the redesign note is parked outside the actionable board.
4. **Dropped fold KNOWN_ISSUE** from handoff.

### Prior session (commits ~467b40c3–da4804f0 on main)

1. **Removed dead `getPauseAdjustedNow` import** from `tools/topology-vscode/src/webview/three/pump.ts` (commit `467b40c3`).

2. **Re-audited 5 cross-cut proposals from `feature-audit.md`**, removing 4 as stale and downgrading 1. Common failure: proposals sized cross-cut weight by surface count rather than per-change edit count.
   - `runStatus` store-subscribe — REMOVED (no prop-drill; one Context consumer; animation path bypasses React).
   - Spec↔flow codegen — REMOVED (adapters already iterate generated `WIRE_PROPS`/`NODE_DEFS`; field-add = 0 adapter edits today).
   - load-spec/load-view unify — REMOVED (`viewer/types.ts` is schema-of-truth, not redundant; can't be inferred from `parse.ts`).
   - validation-flag-colors observe-not-assert — REMOVED (passive consumers, not parallel validators; refactor would increase file count).
   - view-save-on-settle observe-debounce — KEPT, downgraded High → Medium (marginal 3→2 file gain).

3. **Replaced `feature-audit.md` with an HTML audit site** at `docs/planning/visual-editor/feature-audit/` (commit `d8226fd8`). Structure: `index.html` (category tabs + multi-select status filter chips + 3-state cross-cut sort), `data.js` (all features in `window.AUDIT_DATA`), `styles.css`, `features/<slug>.html` per feature. The old `feature-audit.md` is now a 3-line pointer. Viewable via VS Code Simple Browser / Live Preview.

4. **Pruned audit board from 28 → 15 features.** Stable working features with no actionable proposal were removed. Audit board is now actionable-only.

5. **Added `untested` status** (gray badge) for features whose code reads correctly but where user has not hands-on verified visible behavior.

6. **Attempted task branch** `task/spec-flow-codegen` to implement the spec↔flow codegen proposal; immediately closed when concrete simulation confirmed the proposal was stale. Branch deleted local + remote; no commits landed.

### Actionable shortlist from the audit board

The audit site index at `docs/planning/visual-editor/feature-audit/index.html` lists all 15 remaining features. Four are flagged as needs-work:

- **`billboarded-node-labels`** — REWORK. Defects: wrong position relative to nodes, wrong text format, possibly missing label content.
- **`arcball-camera-controls`** — REWORK. Rotation has an issue; click-to-activate for XY drag may be the wrong activation model. Per CLAUDE.md `interaction-control-is-substance` rule, this is substance, not a medium choice.
- **`validation-flag-colors`** — code reads correctly, UNCHECKED (user has not hands-on verified).
- **`two-click-edge-creation`** — code reads correctly, UNCHECKED (user has not hands-on verified).

### Next-task candidates (friction-driven)

1. Diagnose and fix `billboarded-node-labels` (concrete defects reported; branch when repro confirmed).
2. Redesign `arcball-camera-controls` rotation/activation (needs concrete repro before branching).
3. Hands-on verify `validation-flag-colors` and `two-click-edge-creation` in the live editor.
4. Pre-existing test failures (parked from prior session — investigate before the next task branch).

### Historical context — pulse-substrate-transport (merged 2026-05-28, commit range `0572704a`–`2662baa4`)

Substrate-owned pulse transport timing landed end-to-end: `simLatencyMs` flows from Go `PacedWire` → `send` trace event → `pump.ts` → `PulseBead`; latency-live drag working with same-frame TS-local recompute; curve is derived non-React store state; curve constants codegen'd from `curve_params.go` via `gen-node-defs`; visible px/ms genuinely uniform across all wires; TS→Go relationship strictly one-way.

### Build / test gate (last verified 2026-05-28)

- `go build ./... && go test ./...` — all pass.
- `npx tsc --noEmit` — clean.
- `npm run build` — `out/webview.js` refreshed (1.1 MB).

### KNOWN ISSUES

1. **`billboarded-node-labels`** — wrong position, wrong text format, possibly missing content (see audit board).
2. **`arcball-camera-controls`** — rotation issue; activation model uncertain (see audit board).
3. **`validation-flag-colors`** and **`two-click-edge-creation`** — untested in live editor.
4. **Pre-existing test failures** — parked; investigate before next task branch.
5. **Drag-to-wire** — port-targeted edge creation by dragging from a port handle; parked.
6. **Port-incompat wiring** — no visual guard when connecting incompatible port types; parked.
7. **Cross-cut refactors (remaining)** — (a) per-kind spec↔flow adapters to isolate blast radius in `spec-to-flow.ts` (preemptive — only 4 kinds, no per-kind switch today); (b) explicit viewer-state derivation from spec (6 of 8 fields genuinely independent; main hazard is the `spec-to-flow.ts:41–45` round-trip invariant — pin with a test, not a refactor). view-save-on-settle is Medium (3→2 file gain).

### Key files

- `tools/topology-vscode/src/webview/three/ThreeView.tsx` — orchestrator; render in `scene-content.tsx`, interaction in `interaction-controls.ts`, camera widgets in `camera-ui.tsx`, math in `geometry-helpers.ts`.
- `tools/topology-vscode/src/webview/three/store.ts` — thin Zustand store; fade in `fade-actions.ts`, edge creation in `edge-creation.ts`.
- `tools/topology-vscode/src/webview/three/fade.ts` — `computeFade` fixpoint (render-mask only).
- `tools/topology-vscode/src/schema/node-defs.ts` — generated node defs (spec layer); `src/schema/parse-spec.ts` — `requiredInputDiagnostics` (editor-diagnostic only).
- `nodes/Wiring/paced_wire.go` — `ArcLength`, `SimLatencyMs`, `PulseSpeedWuPerMs`; `faded` flag + `SetFaded` + `Send` gate.
- `nodes/Wiring/stdin_reader.go` — `NodeMoveRegistry`; node-move IPC → `PacedWire.ArcLength`/`SimLatencyMs` recompute (silent; no trace event emitted back).
- `nodes/Wiring/loader.go` — threads node positions into wire construction for initial `arcLength`.
- `nodes/input/node.go` — Input node (also serves bootstrap role).
- `docs/planning/visual-editor/feature-audit/index.html` — audit board (15 features, 4 actionable).

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
