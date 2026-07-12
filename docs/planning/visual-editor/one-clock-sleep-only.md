---
branch: task/layout-rebuild-on-domain-network
---

# One clock, sleep-only pacing (decided model)

## The model (David, decided)

- **One clock.** The real (wall) clock only. Delete `FakeClock`. The "human clock
  cycle" is a **single division of the real clock** (real time ÷ `MsPerTick`), not a
  separate clock or a tick-counter to advance.
- **Every node/wire loop is:** do the node's regular update activities (input + output
  ports, including one bead `StepOnce`), **sleep one human clock cycle**, repeat. Beads
  and node advance at the same human-cycle pace because they're the same loop.
- **No concurrency block in the loop.** No `WaitTick`, no `cond.Wait`, no select-on-cond,
  no `Ready()` channel. The sleep is a plain timer (`time.After(tickPeriod)`), ctx-aware.
  It never blocks on pause — so the layout-port drain runs every cycle and drag-while-
  paused moves the node.
- **No `is-clock-running` check in the loop.** The loop always does its activities and
  sleeps. (Pause is `Halt`/`Resume` on the clock, still driven by stdin play/pause and
  still freezing `Tick()`; it is NOT a branch in the loop.)

## What STAYS

- `RealClock.Tick()` — wall-clock-derived integer tick. Still needed: bead placement
  (`placeHeld`/`PlaceDrivenAt`/`placeBeadNoWalkerAt`), stepping (`StepOnceAt`), and bead
  position geometry all key off `t = (Tick()−placementTick)/ticksToCross`.
- `Halt`/`Resume` — play/pause via `stdin_reader.go` (`clk.Halt`/`Resume`), `main.go`.
- `StepOnce`/`StepOnceAt` — the per-cycle non-blocking delivery step (does NOT use
  WaitTick; reads Tick()).

## What GOES

- `FakeClock`, `NewFakeClock`, `AdvanceTicks`, `SetTick`.
- `Clock.WaitTick` (interface + `RealClock.WaitTick`).
- **Blocking delivery path** (decided: retire, StepOnce-only): `DriveBeadsToDelivery`
  (`paced_wire.go:454` WaitTick loop) + `EmitOneDriven`/`DriveAll`/`EmitManyDriven`/
  `PlaceAndDrive`(DeliverOnly). ALL delivery goes through per-cycle `StepOnce`.
- The two gate nodes' chan-mode (`EmitOneDriven`) + `DriveHeld` chan fallback + the
  `WaitTick` field/`defaultPark` on gatecommon — collapse to sleep-only StepOnce.
- All tests depending on FakeClock/WaitTick/AdvanceTicks (delete).

## New primitive

`SleepCycle(ctx context.Context) error` on the (single) clock:
`select { case <-time.After(tickPeriod): return nil; case <-ctx.Done(): return ctx.Err() }`

## Execution passes (keep building between passes where possible)

1. **Add + migrate pacing.** Add `SleepCycle` to RealClock. Swap every one-cycle
   `WaitTick(ctx, Tick()+1)` → `SleepCycle(ctx)` in the 6 node loops + `drive.go` + gate
   `park`. (WaitTick still exists; build stays green.)
2. **Retire blocking delivery.** Rewrite everything that used `DriveBeadsToDelivery`/
   `EmitOneDriven`/`DriveAll` onto per-cycle StepOnce; collapse the two gates + DriveHeld
   fallback to sleep-only; rework `emitRefillSlide` (builders.go:702) to sleep-paced
   frames reading Tick() for the lerp.
3. **Delete.** Remove `WaitTick` (interface + RealClock), `FakeClock`, `AdvanceTicks`/
   `SetTick`. Delete dependent test files; trim clock_realclock_test.go (keep Halt/Resume
   + Tick tests); swap FakeClock→RealClock in LoadTopology-arg-only tests (layout_cascade,
   fanin_travel_time, quantized_layout_phase3, node_move). node_move_test.go:353 relied on
   "a FakeClock that never ticks" for an idle sim — review.
4. Full `scripts/stop-checks.sh` green; commit.

## Blast-radius inventory
See the merge/refactor session; key edit files: clock.go, builders.go, paced_wire.go,
ports.go, gatecommon/gate.go, gatecommon/drive.go, the 6 node loops, gatetesthelper/helper.go.
