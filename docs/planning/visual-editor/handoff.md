# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-06-01 — task/wire-delete-pulse merged to main)

- **Active branch:** `task/timing-window` (now merged up to current `main`, which includes the `task/wire-delete-pulse` merge).
- `task/wire-delete-pulse` is **merged to main** and deleted (local + remote).
- Build/test gate: green at merge (`go build ./... && go test -count=1 ./...`, `npx tsc --noEmit` clean).

### What landed on task/wire-delete-pulse (merged)

- **Deleted wire drops its pulse.** `PacedWire.Delete` clears the wire's pulse state via `resetLocked` (zeroes `pending`/`slot`, sets `hasSend=false`), so a deleted wire carries no pulse and no node receives it. Verified from `.probe/go.jsonl`: a `wire_delete_drop_pulse had_pulse=true` line for inhibitRight0/FromLeft had NO subsequent `wire_recv` for that port.
- **`NotifyDelivered` deleted-guard.** Early return when the wire is deleted, so a late in-flight delivery cannot set `hasSend`. (Defensive; not exercised in the verification run — `notify_on_deleted_wire_ignored=0`.)
- **Verification breadcrumbs** added (via `Trace.Breadcrumb`, keyed by destination node+port): `wire_delete_drop_pulse` (in Delete, before clear), `notify_on_deleted_wire_ignored` (in the guard), `wire_recv` (in ports.go TryRecv on a real receive).
- **DELIBERATELY NOT done:** the `slotReadyCh`→cond receiver rewrite was reverted to keep this branch scoped to pulse-removal only. See next section.

### NEXT task on this branch — task/timing-window

Spec: [timing-window.md](timing-window.md). Read it first; spec-first, no code ahead of confirmation.

Two things remain, in order:
1. **The freeze is NOT fixed yet.** A receiver parked in `PacedWire.Recv` is still orphaned when `Reset`/`Delete` swaps its `slotReadyCh` (the parked goroutine holds the old channel and never wakes on re-add). The pulse-removal merge did not touch the receiver path. The robust-receive rewrite (wait on the durable `hasSend`/cond condition, broadcast on Delete/Reset/Restore/deliver/Done, remove `slotReadyCh`) is the timing-window branch's job.
2. **Coincidence detection** layered on top: the permanent-delete case (an input that genuinely never arrives, so the gate flushes a partial combination after window `W`). Non-blocking poll receive drives the window timer.

### Key files

- `nodes/Wiring/paced_wire.go` — `Recv`/`slotReadyCh` is what the receive rewrite replaces; pulse-removal in `Delete`/`resetLocked` and the `NotifyDelivered` guard already landed.
- `nodes/Wiring/paced_wire_test.go` — add parked-`Recv`-survives-`Reset`/`Delete` cases (select-with-timeout so a hang fails fast).
- `docs/planning/visual-editor/timing-window.md` — this branch's spec.

### Substrate model contract (stable)

See [MODEL.md](../../../MODEL.md#slot-phase-lifecycle). Send rules are node-owned (`node.data.sendRules`: `consumeGated` / `fireAndForget`) and `PacedWire` is pure transport (`WaitConsumed` / `Reset` / `Delete` / `Restore`). `pump.ts` stays render-only.

## Dev-loop

After TS edit: `npm run build` from `tools/topology-vscode/`.
After Go change: `go build ./...` from repo root, `go test ./nodes/Wiring/...`.
To repro / inspect: clear `.probe/*.jsonl`, reload window in VS Code, Run once, inspect logs (`go.jsonl` breadcrumbs: `wire_delete_drop_pulse`, `wire_recv`).

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
