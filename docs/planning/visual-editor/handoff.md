# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md);
this file is the filled-in current state. A fresh AI session should
read this file first (no chat history needed) and proceed.

---

## State at handoff (2026-06-01 — split deleted-wire fix from timing-window)

- **Active branch:** `task/wire-delete-pulse` (branched from `main` HEAD `c0eaee65`).
- **PRIORITY / NEXT task:** the deleted-wire receive fix per
  [wire-delete-pulse.md](wire-delete-pulse.md) — **read that spec first; spec-first, no code ahead of confirmation.**
- Build/test gate: must be **green at merge** (`go build ./... && go test -count=1 ./...`, `npx tsc --noEmit` clean). Likely Go-only — no TS change expected.

### Why this branch (the fix)

The **delete+re-add freeze on `main`** is a standalone receive bug, NOT a
timing-window concern. A node parked in `PacedWire.Recv` waits on a
per-cycle `slotReadyCh`; `Delete`/`Reset`/`Restore` swap that channel,
orphaning the parked receiver so it never wakes even though a new pulse
arrived after re-add (the pulse DID reach the gate — nothing to time out).

This branch enforces the user-stated invariant: **a deleted wire → its
pulse is removed → no node receives it.** Concretely (see spec for full
detail):
- `NotifyDelivered` is a no-op on a `deleted` wire (no late delivery sets
  `hasSend`).
- `Recv` is rewritten to wait on the durable `hasSend` condition via the
  cond variable (not a throwaway `slotReadyCh`); `Delete`/`Reset`/
  `Restore`/`deliverLocked`/`Done` broadcast so a parked `Recv`
  re-checks. A parked `Recv` on a deleted/cleared wire keeps waiting
  (does not receive the removed pulse); after re-add + a fresh delivery
  it wakes and receives. `slotReadyCh` is removed entirely.

### Queued AFTER this — task/timing-window (depends on this)

`task/timing-window` (spec: [timing-window.md](timing-window.md)) layers
coincidence-detection on top of the robust receive landed here. It
handles only the **permanent-delete** case (an input that genuinely
never arrives, so the gate flushes a partial combination after `W`). Its
non-blocking poll receive is needed only to drive the window timer — it
does NOT fix the freeze (that is this branch). Do not start it until
`task/wire-delete-pulse` merges.

### Key files

- `nodes/Wiring/paced_wire.go` — pure transport; the blocking `Recv` /
  `slotReadyCh` swap is what this branch rewrites. `NotifyDelivered`
  gains the `deleted` guard.
- `nodes/Wiring/paced_wire_test.go` — transport tests; add the
  parked-`Recv`-survives-`Reset`/`Delete` and late-`NotifyDelivered`
  cases (select-with-timeout so a hang fails fast).
- `docs/planning/visual-editor/wire-delete-pulse.md` — this branch's spec.
- `docs/planning/visual-editor/timing-window.md` — queued-after spec.

### Substrate model contract (stable)

See [MODEL.md](../../../MODEL.md#slot-phase-lifecycle). Send rules are
node-owned (`node.data.sendRules`: `consumeGated` / `fireAndForget`) and
`PacedWire` is pure transport (`WaitConsumed` / `Reset` / `Delete` /
`Restore`). `pump.ts` stays render-only. This branch keeps that contract;
it only makes the receive path reset-robust.

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
