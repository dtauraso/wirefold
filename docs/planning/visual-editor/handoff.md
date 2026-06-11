---
# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md); this file is the
filled-in current state. A fresh AI session should read this first (no chat history
needed) and proceed.

---

## State at handoff (2026-06-11 — on `main`, clean, no task in flight)

All task branches deleted. Latest main: `8e397d34` (merge of dead-code removal). Fresh session starts on `main`.

### Settled architecture (complete — no open items)

- **Go** owns the running model: one clock, pacing/timing, bead transport+delivery, bead **progress (fraction t)**, node-local held state, firing rules, node positions (held, re-emitted on move), per-edge geometry, shading. Self-scheduling per-goroutine nodes + wires; **no central coordinator** — node-move AND fade both via key→channel dispatch through `MoveDispatch`. Zero central fan-out.
- **TS** is render-only (viewpoint): camera, projection, raycast picking, GPU render; places the bead at Go's fraction along Go's segment. Sends fire-and-forget `edit` (create/update/delete/fade) + play/pause. Owns interaction, NOT position data. Geometry helpers are store-readers with startup-only local fallback.
- **Bridge:** Go→TS trace stream (9 active kinds, no fade trace); TS→Go fire-and-forget. The MoveDispatch reader is a pure mail-sorter.
- **MODEL.md + CLAUDE.md confirmed drift-free.**

### This session's work (all merged to main)

**Dead-code removal F1–F4 (net −277 lines):**

- **F1:** Deleted orphaned canonical/edge-keyed trace resolver (`Trace/Resolve.go`, `WriteCanonicalJSONL`, `marshalCanonicalEvent`) — the TS-simulator consumer was gone. `Event.Edge` was **kept** (live for geometry events).
- **F2:** Removed write-only `Event.hasValue` (all 6 set-sites deleted; no read-sites existed).
- **F3:** Removed dead `arrowStyle`/`concurrent` wire props + corresponding `specEdge` fields — never read downstream.
- **F4:** Dropped orphan `NODE_DEFS` fields from per-kind-component and required-input-enforcement eras via generator + regen. `isMulti` **kept** (live consumer).
- Dropped audit scaffolding (planning md + HTML report).

**Branch hygiene:** deleted all merged task branches (`editor-3d-plan`, `full-code-audit`, `inhibitright-pseudo`, `partial-feature-audit`). Only `main` remains.

**Memory recorded:** `project_superseded_arch_orphans` — orphaned old-architecture plumbing is drift bait; delete-don't-revive.

### Key lessons from this session

- **Dead-on-main ≠ patch-applies-cleanly.** A stale removal branch had to be brought current (merge main in, finish F2's extra sites, regen F4) rather than cherry-picked.
- **Verify the real build independently.** Mid-merge IDE diagnostics were stale LSP cache; subagent "all green" was double-checked against an actual `go build`/`go test`.

### Carry-forward facts

- **Per-goroutine model complete; zero central fan-out.** node-move + fade both go through `MoveDispatch` (key→channel). create/delete are single-target (one wire). play/pause is the intentional global gate (one clock). loader/clock/trace-sink/dispatch-router are shared facts/conduits, not coordinators.
- **Go holds node position**, re-emits node-geometry on move; render is Go-authoritative (node body, wire, bead all from Go's stream). The editor owns interaction/viewpoint, not position data.
- **Pulse placement** = `lerp(edge-geometry segment, Go's fraction)` — same store the tube reads, so the bead can't leave the wire.
- **No fade trace in bridge.** Fade behavior is `PacedWire.SetFaded` gating `Send`; no trace event emitted.
- **Parser-parity is a recurring trap:** when changing a TS→Go message shape, update `parseEdit` in `messages.ts` AND the Go stdin-reader struct in the same change, or the message is silently dropped (no error). See `feedback_schema_parser_parity`.
- **Two-process editor:** changing extension-host code (`messages.ts`/`handle-message.ts`/`extension.ts`) needs **Developer: Reload Window**; webview-only changes refresh on reopen. See `feedback_two_process_editor_reload`.
- **Bead-item chain rejected** (`project_wire_is_straight_line_not_chain`) — don't re-propose; straightness is non-local → O(N²) follow latency.
- **Probe logs** (`.probe/*.jsonl`) accumulate across sessions; clear them (`: > file`) for a clean diagnostic read.

### Unconfirmed thread (low priority)

A possible TS render-layer node issue was noted — Go's stream is clean (continuous drag path, fades independent of drag frames, no anomaly in position/geometry data). Any residual is TS-side (node mesh store / raycast / camera). Low priority; may already be resolved. If it resurfaces, fix is in TS, not Go.

### NEXT

**No in-flight task.** Start fresh on `main` from user-reported friction (log to `docs/planning/visual-editor/session-log.md`, open a fresh `task/<short-kebab>`).

### Active experimental network — 3 edges

`topology.json`: `in08` (Input init `[0,1]`), `i0` (ChainInhibitor), `i1` (ChainInhibitor). Edges `in08→i0`, `i0→i1`, `i0→in08` (feedback). All animate. File is skip-worktree.

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
