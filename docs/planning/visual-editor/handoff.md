# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-05-26 — fade feature implemented; undo/redo stack removed; bash approval guard added)

**Active branch:** `task/undo-redo`. Branched off main; all of main is merged in (branch is ahead, nothing behind).

Branch-local doc: `docs/planning/visual-editor/fade.md` (frontmatter `branch: task/undo-redo`). Will be stripped by `tools/strip-branch-local-docs.sh` before any merge unless relocated. Decide relocate-vs-strip before merging.

### What shipped on this branch

**1. Fade feature (replaces undo/redo with a non-destructive mask).** Spec: `fade.md`. Fade is a per-node/per-edge mask; topology is unchanged. It is a pure START GATE — in-flight pulses finish; faded nodes/edges start no new pulse. Symmetric across the boundary: TS suppresses animation, Go suppresses firing. End-to-end data flow, all landed and verified headlessly (Go tests, tsc, npm build, 6 fade unit tests, parity + vocab guards green):
- `e527d07f` PacedWire fade gate — `Send` skips on a faded wire (+ tests). No `Drop` primitive; in-flight values are NOT interrupted.
- `68b402b0` `"fade"` stdin message applies the FULL faded-edge set to the WireRegistry wholesale (idempotent). Go only ever receives faded EDGE ids; node-fade expands to edges in TS.
- `26159aa8` TS state model (`faded` on NodeData/EdgeData), `fade.ts` fixpoint `computeFade`, store `toggleFade` action, bridge emit + host relay (`handle-message.ts`).
- `c7650e98` muted rendering (0.25 opacity) + `f` hotkey to fade the current selection.
- Fixpoint rules: faded node → all incident edges faded; node with zero non-faded edges auto-fades; spreads/contracts to a fixpoint. Unfade by direct unfade OR adding an edge (new edges are non-faded).

**2. Undo/redo stack removed** (`1975d655`). Deleted `state/history.ts`; removed Cmd/Ctrl+Z keybind, `pushSnapshot` calls, and `restoreNodesEdges`. `mutateViewer` keeps mutating+persisting, no longer snapshots. Fade is the replacement for the reversible-navigation role; delete is the (terminal) cleanup pass.

**3. Bash approval guard** (`tools/bash-approve-guard.sh`, committed on main, merged here). PreToolUse(Bash) hook in `.claude/settings.json`. Three tiers over the full command string: CATASTROPHIC → `deny` (hard block); DESTRUCTIVE/NETWORK → pass-through (no decision, native prompt handles them so "always allow" persists); otherwise → `allow` (silent). Static `permissions.allow` list removed (hook supersedes it). The `>` overwrite matcher was dropped; `git push/pull/fetch` auto-allow; `git push --force` and `git clone` still prompt. Edit the pattern arrays to tune. Hook is live (confirmed this session).

### Open decisions / next

**(a) Live editor verification of fade — NOT done.** Everything passed headless, but no one has driven the editor: reload the VS Code window, select a node/edge, press `f`, Run, and confirm the right element mutes and Go stops emitting on it (faded wire carries no new pulse; in-flight finishes). This is the first thing to do.

**(b) Orphaned test `tools/topology-vscode/test/spec-undo-invariant.test.ts`** — 10 failures, PRE-EXISTING (broken on main since 2026-05-19 commit 89125567 deleted the spec-state functions it imports; `setSpec is not a function`). It tests a spec-undo system that no longer exists. Delete it (and any dangling spec-history references) — unrelated to this branch's work but should be cleaned before merge.

**(c) fade.md fate:** branch-local (frontmatter `branch: task/undo-redo`); will be stripped on merge. Relocate to survive if it should land on main.

**(d) topology.json working-tree modification:** node-drag view positions, modified-but-uncommitted INTENTIONALLY. Do NOT stage or discard.

### Key files

- `nodes/Wiring/paced_wire.go` — `faded` flag + `SetFaded` + skip-at-top-of-`Send` gate.
- `nodes/Wiring/stdin_reader.go` + `loader.go` (`WireRegistry.ForEach`) — `"fade"` message applies the edge set.
- `tools/topology-vscode/src/webview/three/fade.ts` — pure `computeFade` fixpoint.
- `tools/topology-vscode/src/webview/three/store.ts` — `directlyFadedNodes/Edges` + `toggleFade` + bridge emit.
- `tools/topology-vscode/src/webview/three/ThreeView.tsx` — muted render + `f` hotkey.
- `tools/topology-vscode/src/extension/handle-message.ts` — relays `"fade"` to Go stdin.
- `tools/topology-vscode/src/webview/types.ts` — `faded?: boolean` on NodeData/EdgeData.
- `docs/planning/visual-editor/fade.md` — branch-local fade spec.
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
