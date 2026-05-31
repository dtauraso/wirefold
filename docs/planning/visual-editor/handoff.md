# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-31 — edge-surface-rendering merged to main)

- **Active branch:** `main` HEAD `05f9222e` (Merge task/edge-surface-rendering). No task branch in flight.
- Working tree: `topology.json` modified + `.claude/scheduled_tasks.lock` untracked (both pre-existing/unrelated; not staged).
- Build/test gate: baseline is green — `go test ./...` all pass; `npx tsc --noEmit` clean; `npm run build` refreshes `out/webview.js`.

### What merged this session

- **`task/edge-surface-rendering`** — edge tubes now start at the node sphere SURFACE instead of the node center. The curve is still defined center-to-center (latency/pulse-bead math is unchanged and stays center-based); the rendered tube is trimmed to the [t0,t1] range outside each node's `nodeRadius`, with the exact surface crossing found by linear interpolation between bracketing samples (no draw-then-delete). Applies uniformly to all edges via the single `SingleEdgeTube` renderer. PulseBead deliberately left traveling the full center-to-center curve (visible from node center) — timing is center-based by design. Edge tube WIDTH is a fixed constant 1.5 (does not scale with node radius like the torus `r*0.08`); user reviewed and chose to leave as-is this session. Commits: `6818a3e8`, `93a03fca`, `a1ab11b8`, `4a460a02`, merge `05f9222e`.

### Prior merge context (still relevant)

**Merge `68c4da40` from `task/arcball-rework` (deleted):** camera rotation on single empty-space drag, implemented as two decoupled in-plane cylinders (horizontal->world Y, vertical->world X) pivoting on the selected node (else screen-center on z=0 plane); rigid rotation keeps the pivot pixel-fixed; tilt persists across reload. Pinch-zoom reworked to zoom-to-cursor and xy pan made tilt-aware so all three gestures are consistent at any tilt. z-spin (third axis) considered and declined. VERIFIED live by user.

**Merge `31a46dca` from `task/pinch-zoom-rework` (deleted):** reworked pinch-zoom to multiplicative exponential zoom on camera height above z=0 plane. Single knob `ZOOM_BASE=1.01`. Now superseded by zoom-to-cursor from `task/arcball-rework`.

**Merge `bda401e1` from `task/xy-pan-camera` (deleted):** two-finger scroll pans camera in world x/y; arcball rotation and dwell→PanPad removed; camera locked square-on. Now superseded by tilt-aware pan from `task/arcball-rework`.

**Merge `98584a6f` from `task/billboard-name-kind-only` (deleted):** simplified the top-of-node billboard to two static lines and moved per-instance state into an HTML overlay; ripped out all pseudocode plumbing. Net: -3408 lines, +98 lines. Audit board updated (commit `373c7f7b`).

### Next-task candidates (friction-driven)

Outstanding editor features are tracked on the audit board at `docs/planning/visual-editor/feature-audit/index.html` (12 features with status columns). Pick friction-driven from there. The one cross-cut item not on the board: per-kind spec↔flow adapters / explicit viewer-state derivation / view-save-on-settle (view-save-on-settle also appears on the board as half-wired).

### Historical context — pulse-substrate-transport (merged 2026-05-28, commit range `0572704a`–`2662baa4`)

Substrate-owned pulse transport timing landed end-to-end: `simLatencyMs` flows from Go `PacedWire` → `send` trace event → `pump.ts` → `PulseBead`; latency-live drag working with same-frame TS-local recompute; curve is derived non-React store state; curve constants codegen'd from `curve_params.go` via `gen-node-defs`; visible px/ms genuinely uniform across all wires; TS→Go relationship strictly one-way.

### Build / test gate (last verified 2026-05-31 post edge-surface-rendering merge)

- `go build ./... && go test ./...` — all pass.
- `npx tsc --noEmit` — clean.
- `npm run build` — `out/webview.js` refreshed (1.1 MB).

### KNOWN ISSUES

1. **Cross-cut refactors (remaining)** — (a) per-kind spec↔flow adapters to isolate blast radius in `spec-to-flow.ts` (preemptive — only 4 kinds, no per-kind switch today); (b) explicit viewer-state derivation from spec (6 of 8 fields genuinely independent; main hazard is the `spec-to-flow.ts:41–45` round-trip invariant — pin with a test, not a refactor). view-save-on-settle is Medium (3→2 file gain).

### Key files

- `tools/topology-vscode/src/webview/three/ThreeView.tsx` — orchestrator; render in `scene-content.tsx`, interaction in `interaction-controls.ts`, camera widgets in `camera-ui.tsx`, math in `geometry-helpers.ts`. Top-of-node billboard renders the two-line `label/id + kind` pill; in-node value overlay is delegated to `node-override-text.ts`.
- `tools/topology-vscode/src/webview/three/interaction-controls.ts` — `onPointerDown` captures the rotation pivot+snapshot; `onPointerMove` `"rotating"` phase runs the two-cylinder rotation; `onWheelNative` ctrl branch = zoom-to-cursor, else branch = tilt-aware plane-unproject pan.
- `tools/topology-vscode/src/webview/three/scene-content.tsx` — `CameraRefBridge`: tilt now persists across reload (saved quaternion restored). `SingleEdgeTube`: surface-trim `useMemo` computes t0/t1 by linear interpolation between bracketing curve samples at each node's `nodeRadius`.
- `tools/topology-vscode/src/webview/three/camera-ui.tsx` — `HomeButton`: re-levels camera to look straight at z=0 plane.
- `tools/topology-vscode/src/webview/three/node-override-text.ts` — HTML-projected in-node overlay pill for per-instance values (Input `init`, ChainInhibitor `state.held`); other kinds render nothing. Uses the existing screen-projection pattern, not drei.
- `tools/topology-vscode/src/webview/three/store.ts` — thin Zustand store; fade in `fade-actions.ts`, edge creation in `edge-creation.ts`.
- `tools/topology-vscode/src/webview/three/fade.ts` — `computeFade` fixpoint (render-mask only).
- `tools/topology-vscode/src/schema/node-defs.ts` — generated node defs (spec layer; `pseudo`/`sublabel` fields removed); `src/schema/parse-spec.ts` — `requiredInputDiagnostics` (editor-diagnostic only).
- `tools/topology-vscode/src/webview/three/geometry-helpers.ts` — `surfacePointForNodes` returns node center (curve is center-to-center); `nodeRadius` formula: `min(w,h)/4`.
- `nodes/Wiring/paced_wire.go` — `ArcLength`, `SimLatencyMs`, `PulseSpeedWuPerMs`; `faded` flag + `SetFaded` + `Send` gate.
- `nodes/Wiring/stdin_reader.go` — `NodeMoveRegistry`; node-move IPC → `PacedWire.ArcLength`/`SimLatencyMs` recompute (silent; no trace event emitted back).
- `nodes/Wiring/loader.go` — threads node positions into wire construction for initial `arcLength`.
- `nodes/input/node.go` — Input node (also serves bootstrap role).
- `docs/planning/visual-editor/feature-audit/index.html` — audit board (13 features after billboarded-labels + sublabel-inline-edit removal).

### Substrate model contract (stable)

See [MODEL.md](../../../MODEL.md#slot-phase-lifecycle). Fade did not change the model: it is a start-gate on `Send`, no new `PacedWire` op, slot-phase/AND-gate/backpressure untouched. `pump.ts` stays render-only. The billboard / in-node overlay changes are pure render — no substrate touch. Edge-surface trimming is purely visual (geometry only); the center-to-center curve and all timing math are unchanged.

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
