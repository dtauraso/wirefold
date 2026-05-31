# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-30 — pan-only square-on camera + pinch-zoom merged to main)

- **Active branch:** `main`. In sync with origin/main at merge commit `bda401e1`. No task branch in flight. Branch `task/xy-pan-camera` merged and deleted (local + remote).
- Working tree: `topology.json` modified (pre-existing, untouched across this session and the prior pan-camera session; still unstaged).
- Build/test gate verified at merge: `tsc --noEmit` clean, `npm run build` clean (1.1 MB webview.js), `go build ./... && go test ./...` all pass.

### What merged (merge bda401e1 — branch task/xy-pan-camera, deleted)

- Two-finger trackpad scroll pans the camera in world x/y (`interaction-controls.ts` `onWheelNative`).
- One-finger arcball rotation and dwell→PanPad removed; RollSlider/PanPad dead code deleted.
- Camera locked square-on: saved tilt quaternion no longer restored on load (`scene-content.tsx` `CameraRefBridge`); Home button re-levels to look straight at z=0 plane (`camera-ui.tsx` `HomeButton`).
- Pinch-to-zoom (wheel+ctrlKey → dolly) retained but **FLAGGED FOR REWORK** in the audit (slug `pinch-zoom`): on the square-on camera it should be a straight z-axis zoom toward cursor/plane; feel/speed unverified.
- Audit: `xy-drag` = working/verified; obsolete `arcball-rotation` + `gesture-activation-model` records and detail pages removed; new `pinch-zoom` record added (needs-rework).

### Prior merge context (still relevant)

**Merge `98584a6f` from `task/billboard-name-kind-only` (deleted):** simplified the top-of-node billboard to two static lines and moved per-instance state into an HTML overlay; ripped out all pseudocode plumbing. Net: -3408 lines, +98 lines. Audit board updated (commit `373c7f7b`).

### Actionable shortlist from the audit board

The audit site index at `docs/planning/visual-editor/feature-audit/index.html` lists the remaining features.

- **`xy-drag`** — DONE / VERIFIED. Two-finger trackpad scroll pans camera in world x/y; camera square-on, never tilts. Arcball rotation and dwell-pan removed.
- **`validation-flag-colors`** — code reads correctly, UNCHECKED (not hands-on verified).
- **`two-click-edge-creation`** — code reads correctly, UNCHECKED (not hands-on verified).

### Next-task candidates (friction-driven)

1. **`pinch-zoom` rework** — square-on camera needs straight z-axis zoom toward cursor/plane; feel/speed unverified; audit record `pinch-zoom` flagged needs-rework.
2. Hands-on verify `validation-flag-colors` and `two-click-edge-creation` in the live editor.
3. Pre-existing test failures (parked from prior session — investigate before the next task branch).

### Historical context — pulse-substrate-transport (merged 2026-05-28, commit range `0572704a`–`2662baa4`)

Substrate-owned pulse transport timing landed end-to-end: `simLatencyMs` flows from Go `PacedWire` → `send` trace event → `pump.ts` → `PulseBead`; latency-live drag working with same-frame TS-local recompute; curve is derived non-React store state; curve constants codegen'd from `curve_params.go` via `gen-node-defs`; visible px/ms genuinely uniform across all wires; TS→Go relationship strictly one-way.

### Build / test gate (last verified 2026-05-29)

- `go build ./... && go test ./...` — all pass.
- `npx tsc --noEmit` — clean.
- `npm run build` — `out/webview.js` refreshed (1.1 MB).

### KNOWN ISSUES

1. Camera is pan-only square-on (merged). Pinch-zoom dolly needs rework — should be straight z-axis zoom toward cursor.
2. **`validation-flag-colors`** and **`two-click-edge-creation`** — untested in live editor.
3. **Pre-existing test failures** — parked; investigate before next task branch.
4. **Drag-to-wire** — port-targeted edge creation by dragging from a port handle; parked.
5. **Port-incompat wiring** — no visual guard when connecting incompatible port types; parked.
6. **Cross-cut refactors (remaining)** — (a) per-kind spec↔flow adapters to isolate blast radius in `spec-to-flow.ts` (preemptive — only 4 kinds, no per-kind switch today); (b) explicit viewer-state derivation from spec (6 of 8 fields genuinely independent; main hazard is the `spec-to-flow.ts:41–45` round-trip invariant — pin with a test, not a refactor). view-save-on-settle is Medium (3→2 file gain).

### Key files

- `tools/topology-vscode/src/webview/three/ThreeView.tsx` — orchestrator; render in `scene-content.tsx`, interaction in `interaction-controls.ts`, camera widgets in `camera-ui.tsx`, math in `geometry-helpers.ts`. Top-of-node billboard renders the two-line `label/id + kind` pill; in-node value overlay is delegated to `node-override-text.ts`.
- `tools/topology-vscode/src/webview/three/interaction-controls.ts` — `onWheelNative`: two-finger scroll → world x/y pan; ctrlKey branch → dolly (pinch-zoom, needs rework).
- `tools/topology-vscode/src/webview/three/scene-content.tsx` — `CameraRefBridge`: square-on lock, saved tilt quaternion no longer restored on load.
- `tools/topology-vscode/src/webview/three/camera-ui.tsx` — `HomeButton`: re-levels camera to look straight at z=0 plane.
- `tools/topology-vscode/src/webview/three/node-override-text.ts` — HTML-projected in-node overlay pill for per-instance values (Input `init`, ChainInhibitor `state.held`); other kinds render nothing. Uses the existing screen-projection pattern, not drei.
- `tools/topology-vscode/src/webview/three/store.ts` — thin Zustand store; fade in `fade-actions.ts`, edge creation in `edge-creation.ts`.
- `tools/topology-vscode/src/webview/three/fade.ts` — `computeFade` fixpoint (render-mask only).
- `tools/topology-vscode/src/schema/node-defs.ts` — generated node defs (spec layer; `pseudo`/`sublabel` fields removed); `src/schema/parse-spec.ts` — `requiredInputDiagnostics` (editor-diagnostic only).
- `nodes/Wiring/paced_wire.go` — `ArcLength`, `SimLatencyMs`, `PulseSpeedWuPerMs`; `faded` flag + `SetFaded` + `Send` gate.
- `nodes/Wiring/stdin_reader.go` — `NodeMoveRegistry`; node-move IPC → `PacedWire.ArcLength`/`SimLatencyMs` recompute (silent; no trace event emitted back).
- `nodes/Wiring/loader.go` — threads node positions into wire construction for initial `arcLength`.
- `nodes/input/node.go` — Input node (also serves bootstrap role).
- `docs/planning/visual-editor/feature-audit/index.html` — audit board (13 features after billboarded-labels + sublabel-inline-edit removal).

### Substrate model contract (stable)

See [MODEL.md](../../../MODEL.md#slot-phase-lifecycle). Fade did not change the model: it is a start-gate on `Send`, no new `PacedWire` op, slot-phase/AND-gate/backpressure untouched. `pump.ts` stays render-only. The billboard / in-node overlay changes are pure render — no substrate touch.

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
