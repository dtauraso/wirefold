# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-26 — task/undo-redo merged to main; no task in flight)

- **Active branch:** `main`. `task/undo-redo` was merged via `884eeaa1` (merge commit) and deleted locally + remote. Working tree: only `topology.json` is modified (intentional node-drag positions — do NOT stage or discard).
- The fade feature and this session's editor UX work are fully on main. No task is in flight; pick from KNOWN ISSUES below or wait for new friction.

### What's on main (this session)

- **Fade feature:** per-node/edge non-destructive mask; pure START GATE (Go suppresses `Send` on faded wires, TS suppresses pulse animation). `computeFade` fixpoint in `three/fade.ts` (Rule1: faded node fades its edges; Rule3: node auto-fades when all incident edges faded). Persisted to `topology.json#view`.
- Fade toggle keys off VISIBLE state (`data.faded`), not direct-set membership.
- **Node-unfade = reverse-playback PATH walk:** unfade the node, its most-recently-faded incident edge, that edge's far node, then continue along each node's most-recent faded edge until the chain ends. Edge-unfade clears the edge + both endpoint nodes. Fade order tracked in `fadeEdgeOrder` (store) and persisted.
- **Selection halos:** edge + node selection halos (orange-red `#ff5a00`; edge halo is also the wide clickable pick area, radius 5).
- **Interaction model:** click = select (free re-selection); drag node→empty = move node; drag node→another node = wire (`createEdge`). Old click-to-arm connect-mode + green banner REMOVED.
- **Undo/redo stack removed** (fade replaces it). Orphaned undo/spec tests deleted; 6 stale test import paths fixed.

### KNOWN ISSUES (candidate next work)

1. **Drag-to-wire edge creation is NON-FUNCTIONAL** (shipped as-is by user decision). Dragging node A onto node B does not create an edge. Unconfirmed root cause: likely `createEdge` (`store.ts`) silently returns null when auto-picked port handles resolve to null (node kinds with empty inputs/outputs in `NODE_DEFS`), and the call site (`ThreeView.tsx` pointer-up drag branch) ignores the return value. Could also be the `nodesOnly`/`excludeId` release pick. Confirm with a runtime breadcrumb before fixing. **This is the top open item.**
2. Node-to-node wiring fails for port-incompatible node kinds (same `createEdge` auto-pick path).
3. **5 pre-existing behavioral test failures** (parser/schema/fold), unrelated to fade/editor work — triage one at a time:
   - `parseSpec.test.ts` — 2: legacy `timing.steps` not dropped; legend bad-kind not rejected.
   - `diff-core.test.ts` — cascades from the `parseSpec` fixture.
   - `fold.test.ts` — "expanded fold emits a frame".
   - `contracts/topology-edge-handles.test.ts` — `topology.json` references kinds `InhibitRightGate`/`ReadGate` absent from `NODE_DEFS` (data drift).
   - `contracts/trace-event-fields.test.ts` — `"done"` kind vs fixture.
4. **Junction-click ambiguity:** overlapping edge pick-tubes near a node junction can mis-pick; click mid-span.

### Key files

- `nodes/Wiring/paced_wire.go` — `faded` flag + `SetFaded` + `Send` gate.
- `nodes/Wiring/stdin_reader.go`, `loader.go` — `"fade"` message applies edge set.
- `tools/topology-vscode/src/webview/three/fade.ts` — `computeFade` fixpoint.
- `tools/topology-vscode/src/webview/three/store.ts` — `directlyFadedNodes/Edges` + `fadeEdgeOrder` + `toggleFade` (path-walk unfade) + `applyFade` + `createEdge`.
- `tools/topology-vscode/src/webview/three/ThreeView.tsx` — render (halos), `pickRequest` (`excludeId`/`nodesOnly`), `useInteractionControls` (click-select / drag-move / drag-to-wire).
- `tools/topology-vscode/src/webview/state/viewer/types.ts` — `ViewerState` fade fields (`directlyFadedNodes`/`directlyFadedEdges`/`fadeEdgeOrder`) + `parseViewerState`.

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
