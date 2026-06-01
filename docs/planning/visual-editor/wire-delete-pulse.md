---
branch: task/wire-delete-pulse
---

# Deleted-Wire Pulse Removal + Robust Receive — Spec

## Invariant (user-stated)
When a wire is removed (edge deleted), the pulse on it must be removed, and **no node is allowed to receive it.**

## Why this is its own fix (split from timing-window)
The delete+re-add freeze is NOT fixed by the timing-window's timeout/clear logic — after re-add the pulse DID reach the gate, so there is nothing to time out. The freeze is caused by the receive mechanism: a node parked in `PacedWire.Recv` waits on a per-cycle `slotReadyCh`; `Delete`/`Reset`/`Restore` swap that channel, orphaning the parked receiver so it never wakes even though a new pulse arrived. That is a standalone bug on `main` and is fixed here. The timing-window feature (separate branch `task/timing-window`) layers coincidence-detection on top and handles the *permanent-delete* case (an input that never arrives).

## Required behavior
1. **Delete removes the pulse.** `PacedWire.Delete` already clears inFlight/pending/slot/hasSend (`resetLocked`). Keep.
2. **No late delivery into a deleted slot.** `NotifyDelivered` on a `deleted` wire must be a no-op — a "delivered" message that arrives after `Delete` (for a bead the pump animated pre-delete) must NOT set `hasSend`; otherwise a removed pulse could still be received. Add a `deleted` guard at the top of `NotifyDelivered`.
3. **Receive is robust to reset (no orphaning).** Rewrite `Recv` to wait on the durable condition `hasSend` via the cond variable, not on a throwaway `slotReadyCh`:
   - `Recv`: `for !hasSend { if ctx.Err() != nil -> ErrCanceled; cond.Wait() }`, with `watchCtx` broadcasting on `ctx.Done` (mirror `Send`). Return `slot` once `hasSend`.
   - `Delete`/`Reset`/`Restore`/`deliverLocked`/`Done` all broadcast (add the broadcast where missing) so a parked `Recv` re-checks. A parked `Recv` on a deleted/cleared wire sees `hasSend=false` and keeps waiting (does NOT receive the removed pulse — satisfies the invariant); after re-add + a new delivery, `hasSend=true` wakes it and it receives.
   - Remove the `slotReadyCh` field and all its uses (`NewPacedWire`, `deliverLocked`, `Done`, `resetLocked`).
4. Net: deleting a wire removes its pulse and no node receives it; a parked receiver is released to re-check instead of orphaned; after re-add the destination receives fresh pulses (freeze gone).

## Non-goals (deferred to task/timing-window)
- Coincidence-detection window / timeout-clear of held inputs (permanent-delete case).
- Non-blocking poll receive (only needed for the window timer).

## Verification
- Unit: a goroutine parked in `Recv`; call `Reset()` (and `Delete()` then `Restore()`), then place+deliver a value → `Recv` returns it, no hang (use a select-with-timeout so a hang fails the test fast).
- Unit: `Delete()` then a late `NotifyDelivered` → `hasSend` stays false, `Recv` does not return a value.
- Live: delete an `inhibitRight0` input edge then re-add → gate receives again, ring resumes, no freeze.

## Build/test
`go build ./... && go test -count=1 ./...`; `npx tsc --noEmit` (likely Go-only — no TS change expected).
