# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-26 — fade bugs fixed + edge selectability + view-file persistence + test cleanup)

**Active branch:** `task/undo-redo`. Branched off main; all of main is merged in (branch is ahead, nothing behind).

`docs/planning/visual-editor/fade.md` was relocated to land on main (branch tag dropped, commit `d6254fac`) — no longer branch-local; will not be stripped on merge.

### What shipped on this branch

**1. Fade feature (replaces undo/redo with a non-destructive mask).** Spec: `fade.md`. Fade is a per-node/per-edge mask; topology is unchanged. It is a pure START GATE — in-flight pulses finish; faded nodes/edges start no new pulse. Symmetric across the boundary: TS suppresses animation, Go suppresses firing. End-to-end data flow, all landed and verified headlessly (Go tests, tsc, npm build, 6 fade unit tests, parity + vocab guards green):
- `e527d07f` PacedWire fade gate — `Send` skips on a faded wire (+ tests). No `Drop` primitive; in-flight values are NOT interrupted.
- `68b402b0` `"fade"` stdin message applies the FULL faded-edge set to the WireRegistry wholesale (idempotent). Go only ever receives faded EDGE ids; node-fade expands to edges in TS.
- `26159aa8` TS state model (`faded` on NodeData/EdgeData), `fade.ts` fixpoint `computeFade`, store `toggleFade` action, bridge emit + host relay (`handle-message.ts`).
- `c7650e98` muted rendering (0.25 opacity) + `f` hotkey to fade the current selection.
- Fixpoint rules: faded node → all incident edges faded; node with zero non-faded edges auto-fades; spreads/contracts to a fixpoint. Unfade by direct unfade OR adding an edge (new edges are non-faded).

**2. Fade bugfixes, edge selectability, and persistence (live-verified by user this session):**
- `d0d3eec7` Three rendering/animation fixes — (A) `data.faded` is now re-derived from the fade sets on every rebuild via a shared `applyFade` helper used by `toggleFade`/`loadSpec`/`loadView` (a topology re-sync no longer clobbers the dim); (B) `clearPulse` on newly-faded edges so a stale in-flight pulse doesn't resume on unfade; (C) `curve.getPointAt` instead of `getPoint` for arc-length-constant pulse speed on bent edges.
- `713c2456` Persist `directlyFadedNodes`/`directlyFadedEdges` to `topology.json#view` (ViewerState + `parseViewerState` validation; `toggleFade` writes via `patchViewerState`+`scheduleViewSave`; `loadView` restores). Fade now survives a FULL window reload, not just topology saves. Toggling fade writes a debounced save to `topology.json#view`.
- `2e471efe` Edge tubes are now selectable (`userData.edgeId` on the tube mesh + `pickRequest` returns it), so `f` fades/unfades a selected edge (previously the raycaster only matched nodes, so `f` never targeted edges).
- `cc9e62e9` + `753792f4` Removed 7 orphaned spec/viewer-undo tests and fixed 6 stale import paths after a module refactor (`rf/` → `state/ops`, `schema/`, `three/`).
- `952fe5c8` Edge selection now has a visual halo: `SingleEdgeTube` renders a concentric larger-radius tube (`haloGeo`, radius 5 vs main 1.5) in saturated orange-red `#ff5a00` at 0.6 opacity (normal blending, DoubleSide, depthWrite=false) when selected; chosen for contrast against the WHITE scene background and the blue edge (gold/additive washed out on white). Fixes the earlier bug where fading "the last edge" unfaded a sibling — root cause was wrong-edge selection with no edge highlight (computeFade was NOT at fault; it only grows the faded set). Caveat: near a node junction overlapping tubes can still mis-pick; click mid-span.
- All fade behavior live-verified by the user: node + edge fade dims and gates pulses, survives window reload, fresh pulse on unfade, constant pulse speed.

**3. Undo/redo stack removed** (`1975d655`). Deleted `state/history.ts`; removed Cmd/Ctrl+Z keybind, `pushSnapshot` calls, and `restoreNodesEdges`. `mutateViewer` keeps mutating+persisting, no longer snapshots. Fade is the replacement for the reversible-navigation role; delete is the (terminal) cleanup pass.

**4. Bash approval guard** (`tools/bash-approve-guard.sh`, committed on main, merged here). PreToolUse(Bash) hook in `.claude/settings.json`. Three tiers over the full command string: CATASTROPHIC → `deny` (hard block); DESTRUCTIVE/NETWORK → pass-through (no decision, native prompt handles them so "always allow" persists); otherwise → `allow` (silent). Static `permissions.allow` list removed (hook supersedes it). The `>` overwrite matcher was dropped; `git push/pull/fetch` auto-allow; `git push --force` and `git clone` still prompt. Edit the pattern arrays to tune. Hook is live (confirmed this session).

### Open decisions / next

**(a) Fade live-verified — DONE.** Node + edge fade dims and gates pulses, survives window reload, fresh pulse on unfade, constant pulse speed. No open items on fade behavior.

**(b) fade.md relocated to main** (branch tag dropped, commit `d6254fac`). No longer branch-local; `strip-branch-local-docs.sh` will not remove it on merge.

**(c) Pre-existing behavioral test failures (NOT fade/undo related, deferred by user).** 5 test files with failures that predate this branch's work — triage one at a time when picked up:
- `parseSpec.test.ts` — 2 cases: legacy `timing.steps` not dropped, legend bad-kind not rejected.
- `diff-core.test.ts` — 8 failures cascading from the `parseSpec` fixture.
- `fold.test.ts` — 1 failure: "expanded fold emits a frame".
- `contracts/topology-edge-handles.test.ts` — 2 failures: `topology.json` references node kinds `InhibitRightGate`/`ReadGate` absent from `NODE_DEFS` (data drift).
- `contracts/trace-event-fields.test.ts` — 1 failure: `TRACE_EVENT_KINDS` has `"done"` but fixture jsonl lacks a done event.

These span parser/schema/fold subsystems; none are fade- or undo-related.

**(d) topology.json working-tree modification:** node-drag view positions, modified-but-uncommitted INTENTIONALLY. Do NOT stage or discard.

### Key files

- `nodes/Wiring/paced_wire.go` — `faded` flag + `SetFaded` + skip-at-top-of-`Send` gate.
- `nodes/Wiring/stdin_reader.go` + `loader.go` (`WireRegistry.ForEach`) — `"fade"` message applies the edge set.
- `tools/topology-vscode/src/webview/three/fade.ts` — pure `computeFade` fixpoint.
- `tools/topology-vscode/src/webview/three/store.ts` — `directlyFadedNodes/Edges` + `toggleFade` + `applyFade` helper (re-derives `data.faded` on every rebuild) + bridge emit.
- `tools/topology-vscode/src/webview/three/ThreeView.tsx` — muted render + `f` hotkey + edge-tube `userData.edgeId` + `pickRequest` edge resolution.
- `tools/topology-vscode/src/webview/state/viewer/types.ts` — `ViewerState` fade fields (`directlyFadedNodes`/`directlyFadedEdges`) + `parseViewerState` validation.
- `tools/topology-vscode/src/extension/handle-message.ts` — relays `"fade"` to Go stdin.
- `tools/topology-vscode/src/webview/types.ts` — `faded?: boolean` on NodeData/EdgeData.
- `docs/planning/visual-editor/fade.md` — fade spec (now main-bound; no longer branch-local).
- `tools/bash-approve-guard.sh` — the approval hook (on main).

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
