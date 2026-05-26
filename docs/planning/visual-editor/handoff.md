# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-26, editor-r3f — rf-retirement COMPLETE (Phases 0-6), live-verified; branch ready for merge to main pending user sign-off)

**Active branch:** `editor-r3f` (long-lived R3F source line; NOT yet merged to main).

### Why 3D

Not cosmetics. The topology genuinely has depth — the wire structures
(inhibitor chain, rings, lateral-inhibition lattices) have real
geometry that the current 2D React Flow canvas flattens into
misleading edge crossings and false adjacencies. The move is about
**representational honesty**: make the rendered structure match the
actual structure.

### Direction (locked)

**ONE 3D view, ONE store.** R3F is THE editor. RF is retired.
RF removal from `main` = the merge event (this branch). This branch carries only R3F things.

Drift guard still applies: interaction CONTROL is substance (no OrbitControls); rendering is medium (R3F yes). zustand is a medium choice (already a dep) — fine to adopt as the store.

### Governing principle (most important — drift guard)

**Interaction CONTROL is substance, not medium.** This is a
**classification clause** of CLAUDE.md's medium-vs-substance rule, NOT
a competing rule. One rulebook, correctly applied.

Decision procedure:

1. Is this rendering/plumbing, or control over the system?
2. Rendering → industry default (react-three-fiber: yes).
3. Control → substance → design from need → apply the
   **recoverable-by-device test**: if a better input device does NOT
   restore a lost capability without changing the design, the loss is
   baked into the design → wrong industry pattern-match → REJECT.

`drei`'s `OrbitControls` FAILS this test (a SpaceMouse still leaves a
fixed pivot and locked roll — the loss is in the design) and must
**NOT** be adopted. Adopting R3F (medium, yes) does not imply adopting
OrbitControls (substance, no).

This principle is also saved in
[`memory/project_interaction_control_is_substance.md`](../../../memory/project_interaction_control_is_substance.md)
(rides to main on merge). Full design lives in
[3d-editor.md](3d-editor.md) (branch-local — does **not** ride the
merge).

### Done — rf-retirement complete (all 6 phases committed + pushed to editor-r3f, all live-verified by user via reload+Run)

- **Phase 0 — pacing handshake restored** (commits `3154816e` + delivered-sender fix): ThreeView PulseBead posts `{type:"delivered", edge: edgeId}` when `t >= 1`, guarded once-per-pulse via `claimDelivered(edgeId, startTime)` in `rf/pulse-state.ts`. Go's `PacedWire.NotifyDelivered()` unblocks; substrate advances at human pace. Pulses animate end-to-end through the chain with no freeze.

- **Phase 1+2 — orphaned rf/ files deleted** (commit `d9e08700`): removed `rf-imperative.ts`, `fire-flash-state.ts`, `slots-state.ts`, `held-values.ts`. `pump.ts` slot/fire branches stubbed to no-ops (no R3F consumer).

- **Phase 3+4 — live R3F infra relocated out of rf/** (commit `531c1575`): `types.ts` → `webview/`; state stores → `webview/state/`; pulse/pump/trace-kinds → `webview/three/`; adapters → `webview/state/adapter/`; `RunButton` → `webview/three/`; node-defs + registry → `webview/schema/`. 12 importers rewired. All 6 `import type ... from reactflow` sites removed; `RFNode`/`RFEdge` now locally defined in `webview/types.ts`. CLAUDE.md path refs updated.

- **Fix — tsc/guard regressions after the move** (commit `a309d838`): `history.ts` `Snapshot` typed to `RFNode<NodeData>[]/RFEdge<EdgeData>[]`; `spec-to-flow.ts:105` cast for fold/note/member nodes; `check-trace-kind-parity.sh` paths updated to `webview/three/`.

- **Phase 5+6 — reactflow npm dep removed** (latest commit on editor-r3f): deleted CSS import in `main.tsx`; removed `reactflow` from `package.json`; pruned 604 lockfile lines including all `@reactflow/*` transitives. Zero `reactflow` importers remain in `src/`.

### Next

**The one remaining step is the `editor-r3f → main` merge.** This IS the "RF removal from main" event per the Direction section. Needs user sign-off before executing. Pre-merge checklist:

1. Run `tools/strip-branch-local-docs.sh editor-r3f` to remove all branch-tagged planning docs (per CLAUDE.md) before the merge commit.
2. Confirm topology.json disposition (see Working-tree state below).
3. After merge: delete `editor-r3f` locally and on remote (per `feedback_branch_cleanup.md` memory).

**Residual cosmetic cleanup (non-blocking, defer until friction):**

- `RFNode`/`RFEdge` type names in `webview/types.ts` still carry the `RF` prefix — rename to `WFNode`/`WFEdge` or `Node`/`Edge` whenever it causes confusion.
- `src/webview/rf/` still holds two live re-export/metadata files (`adapter.ts`, `animation-fields.ts`). The folder name is now a misnomer; these could be relocated to `webview/state/` and `webview/three/` respectively.
- **ThreeView re-renders every frame when idle** (~60fps, no interaction): perf smell, root cause not investigated. Likely an unmemoized store selector or per-frame `setState`. Out of scope until it causes measurable jank.

### Working-tree state

- `topology.json` shows as modified (M) — node-drag positions written to its embedded `view` key (~4 lines: readGate1, inhibitRight0 x/y). Confirm intent before any commit or merge that would sweep it up.

### Separate deferred task (paused — NOT this branch)

Branch `task/inhibitright-pseudo` exists on origin:
**InhibitRightGate pseudo-text projection** (same
Input/ReadGate/ChainInhibitor pattern). Params L/R; semantic "L pass /
R inhibit" → result = `Left==1 && Right==0`. Steps: `cmd/pseudo`
subcommand (render/save) + `nodes/inhibitrightgate/SPEC.md`
`hasPseudo:true` + `handle-message.ts` handler + Go template regen of
`node.go`. **Watch:** apply the ChainInhibitor OutMulti handle-matching
lesson (suffix-strip ToNext0/ToNext1 → base ToNext) if InhibitRightGate
has multiple outputs. Paused while 3D work is in flight.

### Key files

- `tools/topology-vscode/src/webview/three/ThreeView.tsx` — the whole (sole) 3D view: node drag, edge tubes, pointer state machine.
- `tools/topology-vscode/src/webview/three/store.ts` — the single zustand source of truth (nodes/edges/selection, load/save actions).
- `tools/topology-vscode/src/webview/main.tsx` — renders only ThreeView; feeds store on load; hoisted run/save toolbar; posts `{ type: "ready" }` to unblock host load sequence.
- `tools/topology-vscode/src/webview/save.ts`, `tools/topology-vscode/src/webview/three/pump.ts` — read from the store. pump.ts is live in the R3F path (handleTraceEvent wired via main.tsx trace-event case; pulse-state.ts is the R3F pulse read-store; ThreeView PulseBead is the pulse renderer).
- `tools/topology-vscode/src/webview/three/pulse-state.ts` — R3F pulse read-store (getPulseMap, setPulse); live R3F animation infra.
- `tools/topology-vscode/src/webview/types.ts` — local `RFNode`/`RFEdge` type aliases (no reactflow import).
- `tools/topology-vscode/src/webview/state/adapter/{spec-to-flow,flow-to-spec}.ts` — pure adapters, RF-free.
- `tools/topology-vscode/src/webview/rf/` — two residual re-export/metadata files (`adapter.ts`, `animation-fields.ts`); folder name is a misnomer post-retirement.
- `tools/topology-vscode/src/webview/schema/` — node-defs.ts + registry.ts (relocated from rf/).
- `docs/planning/visual-editor/rf-retirement.md` — 6-phase rf/ retirement plan (branch-tagged; strip before merge).

Pseudo files below are for the **deferred** `task/inhibitright-pseudo` branch only, not this one:

- `tools/pseudo/chaininhibitor.go`, `tools/pseudo/readgate.go` — pseudo pattern references
- `cmd/pseudo/main.go` — pseudo subcommand dispatch
- `nodes/inhibitrightgate/{node.go,SPEC.md}` — target to regenerate / mark `hasPseudo:true`
- `tools/topology-vscode/src/extension/handle-message.ts` — handleChainInhibitor{Render,Save} + pseudoTable
- `tools/topology-vscode/src/webview/rf/PseudoPanel.tsx` — double-click-to-edit panel (deleted in Slice 3; must be re-created for this task)

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
