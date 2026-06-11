---
# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md); this file is the
filled-in current state. A fresh AI session should read this first (no chat history
needed) and proceed.

---

## State at handoff (2026-06-11 — on `main`, clean, no task in flight)

All task branches deleted. Latest main: `819fec68` (node rename). Fresh session starts on `main`.

### Settled architecture (complete + merged — no open items)

Go-authoritative per-goroutine model is complete: one clock; pacing/timing,
bead transport+delivery, bead progress (fraction t), node-local held state, firing
rules, node/port positions, and per-edge geometry all owned by Go. node-move AND
fade route via key→channel dispatch through `MoveDispatch` — **zero central fan-out**.
TS is render-only (camera, projection, raycast picking, GPU render); it places the
bead at Go's fraction along Go's segment and sends fire-and-forget `edit`
(create/update/delete/fade) + play/pause. **MODEL.md + CLAUDE.md confirmed drift-free.**

### Work landed on main since last handoff (all merged)

1. **Removed redundant `wire_recv` breadcrumb** (commit `e71d5293`) — it duplicated the `recv` trace event.
2. **Doc hygiene** (commits `9d5642f6`, `5d9f77d7`): stripped 7 leaked branch-local planning docs (their branches had merged+deleted without `strip-branch-local-docs.sh` ever running); deleted `clock-dialog/index.html` (described the superseded TS-authoritative "delivered" model); fixed `fade.md` drift (WireRegistry/delivered → MoveDispatch/SetFaded). Hardened `tools/strip-branch-local-docs.sh` to also match HTML-comment branch tags (`<!-- branch: ... -->`) — the form by which a doc had leaked.
3. **Scene split** (commit `2e684237`): persistence split by ownership.
   - `topology.json` (tracked) = the **DIAGRAM**: nodes, edges, `view.nodes` positions, fade arrays.
   - `topology.scene.json` (gitignored) = the **SCENE** TS owns: camera, camera3d, labelsGlobalHidden.
   - Absent scene file → canvas-default camera + labels shown; diagram intact (reconstitutes to defaults, never harmfully "missing").
   - Go loader unchanged (reads `view.nodes`).
   - `topology.json` is **no longer skip-worktree** — camera churn is gitignored now, so position/fade diffs are real and committable.
   - A round-trip vitest guards positions+fades persistence (a mid-build regression had dropped it; now covered).
4. **Node rename** (commit `819fec68`): active network nodes renamed `in08→1`, `i0→2`, `i1→3` (id is the display name). Propagated to edge endpoints, edge labels (`1To2`, `2To3`, `2FeedbackTo1`), `view.nodes` keys, fade refs, and `topology.inactive.json` cross-refs.

### Active experimental network — 3 edges (feedback ring)

`topology.json`: node `1` (Input, init `[0,1]`, repeat), node `2` (ChainInhibitor),
node `3` (ChainInhibitor). Edges: `1To2` (1→2), `2To3` (2→3), `2FeedbackTo1` (2→1
feedback). A feedback ring; animates. File is tracked normally (not skip-worktree).

### Carry-forward facts

- **`topology.json` is tracked normally now** (not skip-worktree). The gitignored `topology.scene.json` holds camera/labels and reconstitutes to defaults when absent — never harmfully missing.
- **Fading a load-bearing ring edge stalls the whole ring.** Dropping the circulating token leaves every node waiting on an input that never comes; **unfade does NOT revive it** — restart the animation to re-seed from node `1`'s Input init. This is EXPECTED model behavior, not a bug.
- **Parser-parity trap:** when changing a TS→Go message shape, update `parseEdit` in `messages.ts` AND the Go stdin-reader struct in lockstep, or the message is silently dropped. See `feedback_schema_parser_parity`.
- **Two-process editor:** extension-host changes (`extension.ts`/`handle-message.ts`/`messages.ts`) need **Developer: Reload Window**; webview-only changes refresh on reopen. See `feedback_two_process_editor_reload`.
- **Bead-item chain rejected** (`project_wire_is_straight_line_not_chain`) — don't re-propose; O(N²) follow latency.

### Unconfirmed thread (low priority)

A possible TS render-layer node issue was noted — Go's stream is clean; any residual
is TS-side (node mesh store / raycast / camera). Low priority; may already be resolved.
If it resurfaces, fix is in TS, not Go.

### NEXT

**No in-flight task.** Start fresh on `main` from user-reported friction (log to
`docs/planning/visual-editor/session-log.md`, open a fresh `task/<short-kebab>`).

### Dev-loop

- Go: `go build ./...` + `go test -race ./...`. TS (from `tools/topology-vscode/`): `npm run build` (rebuilds extension.js + webview.js) + `npx tsc --noEmit` + `npx vitest run`. Guards: `check-trace-kind-parity.sh`, `check-no-await-on-bridge.sh`, `check-ts-computes-no-geometry.sh`.
- Exercise editor: **Developer: Reload Window** for extension-host changes; reopen file for webview-only.
- No merge to main without explicit sign-off. Delete merged branches without re-asking.

## ALWAYS clause

At end of session, overwrite this file with a freshly-rendered prompt tailored to the
state you're leaving the branch in, and commit on the active branch (main if no task is
in flight). Do not rely on chat history; the next AI may be a fresh model with no
transcript. The rendered handoff must itself contain this same ALWAYS clause so the loop
is self-perpetuating across sessions. Use
[continuation-prompt-template.md](continuation-prompt-template.md) as the structural
source of truth; update the template when an invariant changes.
