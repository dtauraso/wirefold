---
# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md); this file is the
filled-in current state. A fresh AI session should read this first (no chat history
needed) and proceed.

---

## State at handoff (2026-06-11 — branch `task/go-backend-ts-frontend-fixes`, pushed; latest `a18fa000`; tree clean after this commit)

- Branch was **renamed** `task/go-backend-ts-frontend` → `task/go-backend-ts-frontend-fixes` (it carries fixes too). topology.json is skip-worktree.
- This session: post-decentralization **cleanup landed** (`a18fa000`); a **`fade` trace event** added to `.probe/go.jsonl` (`5cf8c14a`); a **node-drag anomaly investigated** (Go stream clean — any residual is TS render-layer, possibly already fixed). Branch-local docs stripped (this commit). **Merge to main is the only remaining item.**

### The arc this session

- **Cleanup (`a18fa000`).** Removed `dbg.flushmove` + `edit-update-forward` breadcrumbs, the thin `applyNodeMove` test shim (façade that let old tests compile over the new dispatch path), and the unused `reg` param in `applyEdit` (`stdin_reader.go`).
- **Fade trace event (`5cf8c14a`).** Added `Trace.Fade` in `Trace/Trace.go`; emitted in `node_move.go` edgeMover fade branch. Shape: `{"kind":"fade","edge":"<id>","faded":bool}` (no `omitempty` on `faded` so unfades appear). TS side updated in lockstep: `trace-kinds.ts` union, `pump.ts` case returns, `messages.ts` discriminated union — **parity guard clean**.
- **Node-drag investigation.** Logs show: drag path continuous (max ~5.6u/frame, no teleport/freeze), fades apply independently of drag (zero shared timestamps), no anomaly in Go's position/geometry data. Conclusion: any residual node misbehavior is TS render-layer, not Go data. User believes it may already be fixed; left open/unconfirmed.
- **Branch-local docs stripped (this commit).** `ownership-audit.html` (tag: -fixes) and `chain-model-two-nodes.md` (tag: original name) removed via `tools/strip-branch-local-docs.sh`.

### The settled architecture (complete)

- **Go** owns the running model: one clock, pacing/timing, bead transport+delivery, bead **progress (fraction t)**, node-local held state, firing rules, node positions (held, re-emitted on move), per-edge geometry, shading. Self-scheduling per-goroutine nodes + wires; **no central coordinator** (node-move + fade both via key→channel dispatch through `MoveDispatch`).
- **TS** is render-only (viewpoint): camera, projection, raycast picking, GPU render; places the bead at Go's fraction along Go's segment. Sends fire-and-forget `edit` (create/update/delete/fade) + play/pause. Owns interaction, NOT position data.
- **Bridge:** Go→TS trace stream (10 kinds incl. `arrive`, `fade`); TS→Go fire-and-forget. The MoveDispatch reader is a pure mail-sorter.

### Active experimental network — 3 edges

`topology.json`: `in08` (Input init `[0,1]`), `i0` (ChainInhibitor), `i1` (ChainInhibitor). Edges `in08→i0`, `i0→i1`, `i0→in08` (feedback). All animate. File is skip-worktree.

### OPEN ITEMS / NEXT

1. **Merge to main (go-auth spec item 6 — the ONLY remaining item):** branch-local docs are now stripped (this commit). Needs **explicit user sign-off** to merge. After merge: delete branch local + remote without re-asking.
2. **Node-drag TS render anomaly (open/unconfirmed):** Go data is clean. If the user observes residual jank after merge, the fix is in the TS render layer, not Go.

### Carry-forward facts

- **Per-goroutine model complete; zero central-fan-out.** node-move + fade both go through `MoveDispatch` (key→channel). create/delete are single-target (one wire). play/pause is the intentional global gate (one clock). loader/clock/trace-sink/dispatch-router are shared facts/conduits, not coordinators.
- **Go holds node position**, re-emits node-geometry on move; render is Go-authoritative (node body, wire, bead all from Go's stream). The editor owns interaction/viewpoint, not position data.
- **Pulse placement** = `lerp(edge-geometry segment, Go's fraction)` — same store the tube reads, so the bead can't leave the wire.
- **Fade trace event:** `{"kind":"fade","edge":"<id>","faded":bool}` in `.probe/go.jsonl`; emitted by `node_move.go` edgeMover; TS pump handles it.
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
