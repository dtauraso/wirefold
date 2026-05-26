# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-26 — editor-r3f MERGED to main (ccbff474); NO task in flight. R3F is sole editor; reactflow retired.)

**Active branch:** `main`. No task branch in flight.

### Last completed — rf-retirement (editor-r3f → main, merged ccbff474)

R3F retirement Phases 0–6 fully complete and merged:

- **Phase 0 — pacing handshake restored:** ThreeView PulseBead posts `{type:"delivered", edgeId}` when `t >= 1`, guarded once-per-pulse via `claimDelivered`. Go's `PacedWire.NotifyDelivered()` unblocks; substrate advances at human pace.
- **Phases 1+2 — orphaned rf/ files deleted:** removed `rf-imperative.ts`, `fire-flash-state.ts`, `slots-state.ts`, `held-values.ts`. `pump.ts` slot/fire branches stubbed to no-ops.
- **Phases 3+4 — live R3F infra relocated out of rf/:** `types.ts` → `webview/`; state stores → `webview/state/`; pulse/pump/trace-kinds → `webview/three/`; adapters → `webview/state/adapter/`; `RunButton` → `webview/three/`; node-defs + registry → `webview/schema/`. All `reactflow` import sites removed; `RFNode`/`RFEdge` now locally defined in `webview/types.ts`.
- **Fix — tsc/guard regressions after the move** (commit `a309d838`): `history.ts` `Snapshot` typed; `spec-to-flow.ts:105` cast for fold/note/member nodes; `check-trace-kind-parity.sh` paths updated.
- **Phases 5+6 — reactflow npm dep removed:** deleted CSS import in `main.tsx`; removed `reactflow` from `package.json`; pruned 604 lockfile lines. Zero `reactflow` importers remain in `src/`.
- **Pre-merge strip:** branch-local `rf-retirement.md` stripped in commit `ff806af8` before merge.
- **Late catch (reinforces `feedback_verify_subagent_commits`):** required import-rewrite edits to `ThreeView.tsx` and `store.ts` were left uncommitted by a subagent, so HEAD briefly didn't compile; fixed in commit `730fcb18` before the merge.

### Next / open work (non-blocking, defer until friction)

- **(a) RF prefix rename:** `RFNode`/`RFEdge` type names in `webview/types.ts` still carry the `RF` prefix — rename to `WFNode`/`WFEdge` or `Node`/`Edge` whenever it causes confusion.
- **(b) rf/ folder misnomer:** `src/webview/rf/` still holds two live files (`adapter.ts` re-export, `animation-fields.ts`). The folder name is a misnomer post-retirement; relocate to `webview/state/` and `webview/three/` respectively, then delete the folder.
- **(c) ThreeView per-frame re-render perf smell:** ThreeView re-renders every frame when idle (~60fps, no interaction). Root cause uninvestigated — likely an unmemoized store selector or per-frame `setState`. Out of scope until it causes measurable jank.
- **(d) topology.json working-tree modification:** node-drag view positions (readGate1, inhibitRight0 x/y) are modified but uncommitted intentionally. Do NOT stage or discard.
- **(e) Separate paused branch:** `task/inhibitright-pseudo` exists on origin — **InhibitRightGate pseudo-text projection** (InhibitRightGate params L/R; semantic "L pass / R inhibit"). Paused while 3D work was in flight; still unstarted.

### Key files

- `tools/topology-vscode/src/webview/three/ThreeView.tsx` — the whole (sole) 3D view: node drag, edge tubes, pointer state machine.
- `tools/topology-vscode/src/webview/three/store.ts` — single zustand source of truth (nodes/edges/selection, load/save actions).
- `tools/topology-vscode/src/webview/main.tsx` — renders only ThreeView; feeds store on load; hoisted run/save toolbar; posts `{ type: "ready" }` to unblock host load sequence.
- `tools/topology-vscode/src/webview/save.ts`, `tools/topology-vscode/src/webview/three/pump.ts` — read from the store.
- `tools/topology-vscode/src/webview/three/pulse-state.ts` — R3F pulse read-store (getPulseMap, setPulse).
- `tools/topology-vscode/src/webview/types.ts` — local `RFNode`/`RFEdge` type aliases (no reactflow import).
- `tools/topology-vscode/src/webview/state/adapter/{spec-to-flow,flow-to-spec}.ts` — pure adapters, RF-free.
- `tools/topology-vscode/src/webview/rf/` — two residual re-export/metadata files (`adapter.ts`, `animation-fields.ts`); folder name is a misnomer post-retirement.
- `tools/topology-vscode/src/webview/schema/` — node-defs.ts + registry.ts (relocated from rf/).

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
