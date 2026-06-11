---
# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md); this file is the
filled-in current state. A fresh AI session should read this first (no chat history
needed) and proceed.

---

## State at handoff (2026-06-11 — branch `task/go-backend-ts-frontend-fixes` MERGED TO MAIN; no task in flight)

This branch's work is **complete and merged**. The next session starts fresh on `main` with no in-flight task.

### What this branch delivered (all committed, merged)

- **Post-decentralization cleanup (`a18fa000`).** Removed `dbg.flushmove` + `edit-update-forward` breadcrumbs, the thin `applyNodeMove` test shim, and the unused `reg` param in `applyEdit` (`stdin_reader.go`).
- **Fade trace event: added then REMOVED.** `5cf8c14a` added `Trace.Fade` (emitted in `node_move.go` edgeMover fade branch, TS pump + `trace-kinds.ts` + `messages.ts` updated in parity). `169404ce` reverted it — used only to confirm behavior was clean, then removed as unneeded bridge surface. Net: **no fade trace in the bridge**. `PacedWire.SetFaded` gating `Send` is unchanged. The `arrive` test-fixture fix that rode in with that commit is kept.
- **Doc-drift audit (`baa59460`, `0f5a6d10`, `cd1532b5`).** Corrected per-node SPEC.md port tables + firing rules; fixed two false "geometry-only at this phase" claims; regenerated `node-defs.ts`; deleted the rejected item-chain HTML page; corrected `go-authoritative-clock/index.html` to the MoveDispatch model and marked items 1–2 DONE.
- **Branch-local docs stripped (`65c870a6`).** `chain-model-two-nodes.md` + `ownership-audit.html` removed via `tools/strip-branch-local-docs.sh`.

### Investigation note

A reported node render anomaly around fade was traced via logs — Go's stream is clean (continuous drag path, fades independent of drag frames, no anomaly in position/geometry data). Any residual is TS render-layer (node mesh store / raycast / camera). User believes it may already be fixed; left unconfirmed, low priority. If it resurfaces, fix is in TS, not Go.

### The settled architecture (complete — no open items)

- **Go** owns the running model: one clock, pacing/timing, bead transport+delivery, bead **progress (fraction t)**, node-local held state, firing rules, node positions (held, re-emitted on move), per-edge geometry, shading. Self-scheduling per-goroutine nodes + wires; **no central coordinator** — node-move AND fade both via key→channel dispatch through `MoveDispatch`. Zero central fan-out.
- **TS** is render-only (viewpoint): camera, projection, raycast picking, GPU render; places the bead at Go's fraction along Go's segment. Sends fire-and-forget `edit` (create/update/delete/fade) + play/pause. Owns interaction, NOT position data. Geometry helpers are store-readers with startup-only local fallback.
- **Bridge:** Go→TS trace stream (9 active kinds, no fade trace); TS→Go fire-and-forget. The MoveDispatch reader is a pure mail-sorter.
- **MODEL.md + CLAUDE.md confirmed drift-free this session.**

### Active experimental network — 3 edges

`topology.json`: `in08` (Input init `[0,1]`), `i0` (ChainInhibitor), `i1` (ChainInhibitor). Edges `in08→i0`, `i0→i1`, `i0→in08` (feedback). All animate. File is skip-worktree.

### NEXT

**No in-flight task.** Start fresh on `main` from user-reported friction (log to `docs/planning/visual-editor/session-log.md`, open a fresh `task/<short-kebab>`).

If the node render-layer anomaly resurfaces: fix is TS-side (node mesh store / raycast / camera), not Go.

### Carry-forward facts

- **Per-goroutine model complete; zero central fan-out.** node-move + fade both go through `MoveDispatch` (key→channel). create/delete are single-target (one wire). play/pause is the intentional global gate (one clock). loader/clock/trace-sink/dispatch-router are shared facts/conduits, not coordinators.
- **Go holds node position**, re-emits node-geometry on move; render is Go-authoritative (node body, wire, bead all from Go's stream). The editor owns interaction/viewpoint, not position data.
- **Pulse placement** = `lerp(edge-geometry segment, Go's fraction)` — same store the tube reads, so the bead can't leave the wire.
- **No fade trace in bridge.** Fade behavior is `PacedWire.SetFaded` gating `Send`; no trace event emitted.
- **Parser-parity is a recurring trap:** when changing a TS→Go message shape, update `parseEdit` in `messages.ts` AND the Go stdin-reader struct in the same change, or the message is silently dropped (no error). See `feedback_schema_parser_parity`.
- **Two-process editor:** changing extension-host code (`messages.ts`/`handle-message.ts`/`extension.ts`) needs **Developer: Reload Window**; webview-only changes refresh on reopen. See `feedback_two_process_editor_reload`.
- **Bead-item chain rejected** (`project_wire_is_straight_line_not_chain`) — don't re-propose; straightness is non-local → O(N²) follow latency.
- **Probe logs** (`.probe/*.jsonl`) accumulate across sessions; clear them (`: > file`) for a clean diagnostic read.

### Dev-loop

- Go: `go build ./...` + `go test -race ./...`. TS (from `tools/topology-vscode/`): `npm run build` (rebuilds extension.js + webview.js) + `npx tsc --noEmit` + `npx vitest run`. Guards: `check-trace-kind-parity.sh`, `check-no-await-on-bridge.sh`, `check-ts-computes-no-geometry.sh`.
- Exercise editor: **Developer: Reload Window** for extension-host changes; reopen file for webview-only.
- topology.json is skip-worktree (in08/i0/i1, 3 edges). No merge to main without explicit sign-off. Delete merged branches without re-asking.

## ALWAYS clause

At end of session, overwrite this file with a freshly-rendered prompt tailored to the
state you're leaving the branch in, and commit on the active branch (main if no task is
in flight). Do not rely on chat history; the next AI may be a fresh model with no
transcript. The rendered handoff must itself contain this same ALWAYS clause so the loop
is self-perpetuating across sessions. Use
[continuation-prompt-template.md](continuation-prompt-template.md) as the structural
source of truth; update the template when an invariant changes.
