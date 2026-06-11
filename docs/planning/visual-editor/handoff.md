---
# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md); this file is the
filled-in current state. A fresh AI session should read this first (no chat history
needed) and proceed.

---

## State at handoff (2026-06-10 — branch `task/go-backend-ts-frontend-fixes`, pushed; latest `968c8e30`; tree clean)

- Branch was **renamed** `task/go-backend-ts-frontend` → `task/go-backend-ts-frontend-fixes` (it carries fixes too). topology.json is skip-worktree.
- This session: explored the **bead-item chain** wire model (built fully) then **reverted** to straight-line PacedWire; a run of **drag/pulse correctness fixes**; then **completed the Go-authoritative per-goroutine model** — decentralized node-move (item 4, the last load-bearing deviation) AND decentralized fade. **Zero central-fan-out remains.**

### The arc this session

- **Bead-item chain — explored + reverted (`e7faf250`).** Built wires as N bead-goroutines relaxing to straight (per-item pulse hops, born/retire, color animation), then reverted. Straightness is endpoint-defined (non-local), so neighbor-only relaxation is **O(N²) follow-latency regardless of goroutines**. Memory: `project_wire_is_straight_line_not_chain`. MODEL.md cleaned (`45521898`).
- **Go-authoritative node position (`fa4eb25f`).** Bug: Go emitted node-geometry only at startup, never on move → dragging didn't update the body. Fix: the move handlers re-emit node-geometry on move; TS renders node/wire/bead all from Go's stream (reverted a TS-local-placement shortcut). Go holds the position; ~1-frame round-trip.
- **Pulse fixes:** in-flight revision preserves the bead's **fraction** (not absolute distance) at uniform speed — no racing (`7bf74109`); walker relaunch tick clamped to ≥now — no t≈0 replay (`8e4c6766`); pulse clears on a new `arrive` trace event at traversal-complete — no lingering at dest port (`2bc98a43`); pulse placed at `lerp(Go segment, Go fraction)` — same store the tube reads, so it stays on the wire as the node moves (`a579b0e6`).
- **Item 4 — node-move decentralized (`a37fae51`).** Replaced central `NodeMoveRegistry.applyNodeMove` with **`MoveDispatch`**: a pure `(key,value)→channels[key]` mail-sorter keyed by node ids AND edge ids. Per-node inboxes (node re-emits its own node-geometry) + per-wire inboxes (each PacedWire owns itself: recomputes its own segment/arc, revises its own in-flight bead under its own lock, emits its own geometry). TS sends keyed entries (moved node + incident edges). Fixed a **parser-parity** bug (`6ec2141c`): `parseEdit` still validated the old update shape → moves silently dropped.
- **Fade decentralized (`968c8e30`).** Last central-fan-out gone — fade routes per-wire via MoveDispatch (each wire sets its own faded flag); `WireRegistry.ForEach` removed. TS sends a per-edge fade map; `parseEdit` + Go reader updated in lockstep.

### The settled architecture (now complete)

- **Go** owns the running model: one clock, pacing/timing, bead transport+delivery, bead **progress (fraction t)**, node-local held state, firing rules, node positions (held, re-emitted on move), per-edge geometry, shading. Self-scheduling per-goroutine nodes + wires; **no central coordinator** (node-move + fade both via key→channel dispatch).
- **TS** is render-only (viewpoint): camera, projection, raycast picking, GPU render; places the bead at Go's fraction along Go's segment. Sends fire-and-forget `edit` (create/update/delete/fade) + play/pause. Owns interaction, NOT position data.
- **Bridge:** Go→TS trace stream (9 kinds incl. `arrive`); TS→Go fire-and-forget. The MoveDispatch reader is a pure mail-sorter.

### Active experimental network — 3 edges

`topology.json`: `in08` (Input init `[0,1]`), `i0` (ChainInhibitor), `i1` (ChainInhibitor). Edges `in08→i0`, `i0→i1`, `i0→in08` (feedback). All animate.

### OPEN ITEMS / NEXT

1. **Reload to verify fade** — extension-host changed (`968c8e30`); **Developer: Reload Window**, confirm fade dimming still works.
2. **Cleanup (not spec):** remove debug breadcrumbs (`dbg.flushmove`, `edit-update-forward`); the thin `applyNodeMove` test façade (a shim so existing tests compile over the new path); the now-unused `x,y,z` on the position trace event (the bead uses fraction); the unused `reg` param in `applyEdit` (`stdin_reader.go:142`).
3. **Merge to main (go-auth spec item 6 — the only remaining spec item):** run `tools/strip-branch-local-docs.sh task/go-backend-ts-frontend-fixes` (strips the chain spec md+html, the go-auth doc, the ownership-audit html), then merge — **needs explicit sign-off**. go-auth spec items 1–5 are DONE.

### Carry-forward facts

- **Per-goroutine model complete; zero central-fan-out.** node-move + fade both go through `MoveDispatch` (key→channel). create/delete are single-target (one wire). play/pause is the intentional global gate (one clock). loader/clock/trace-sink/dispatch-router are shared facts/conduits, not coordinators.
- **Go holds node position**, re-emits node-geometry on move; render is Go-authoritative (node body, wire, bead all from Go's stream). The editor owns interaction/viewpoint, not position data.
- **Pulse placement** = `lerp(edge-geometry segment, Go's fraction)` — same store the tube reads, so the bead can't leave the wire.
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
