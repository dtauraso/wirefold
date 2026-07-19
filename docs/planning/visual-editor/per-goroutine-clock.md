---
branch: task/mutex-shared-services
---

# Per-goroutine clock

## Premise

**Overshoot is not an issue.** A speed change reaches each goroutine when it next wakes,
so the spread across all of them is about one cycle — `MsPerTick = 16`, so ~16 ms on a
control the user operates by hand. Nobody perceives a sixteenth of a second of stagger on
pause. This is settled, not a risk to mitigate.

Everything below follows from accepting that.

## The change

Today one `*RealClock` is constructed in `main.go` and injected into every node, port and
wire. ~16 goroutines call `Tick()` on it continuously; `stdin_reader.go` calls `SetSpeed`
occasionally. Its mutex exists solely because those two collide — it is the widest-fan-in
lock in the system and the only one hit on every cycle by nearly every goroutine.

New model: **every goroutine holds its own clock, reading the system monotonic clock
directly.** Nothing shared, nothing to lock.

The system clock is already global, already consistent, already safe to read
concurrently. It is the shared timeline. Wrapping it in a second shared object and then
guarding that object is the thing being removed.

## Why copies agree

`Tick()` is a pure function of an origin, a speed history, and `now`:

    tick(now) = (accScaled + (now - lastChange) * speed) / tickPeriod

Copies of mutable state drift. Copies of a pure function do not. Two clocks from the same
origin, given the same speed history, return the same value for the same `now` — they
agree by computing, not by consulting shared memory.

## Speed changes: bank at local now

Each clock applies a transition when it hears it, using its own `time.Now()` — exactly
what `SetSpeed` does today, just N times instead of once.

No `effectiveAt`, no transition timestamps, no scheduling. An earlier draft of this plan
built all of that to force every copy to bank at an identical instant; it is unnecessary
under the premise, and it created a worse problem than it solved (retroactive correction
sends a clock backward over ticks it already acted on, which the monotonicity test
forbids). Do not reintroduce it.

### The residual, sized

Banking at local `now` means clocks acquire a permanent offset per speed change:

    offset = (delivery skew) x (new speed - old speed)   ~= 1 tick per change

Sized against this topology: edges are 57-268 wu, so a crossing is tens of ticks. One tick
of offset is **1-2% of a wire per speed change**. Offsets are signed by direction, so
1->2 and 2->1 partly cancel rather than accumulating monotonically.

This matters in exactly one place, which is verified, not theoretical:
`ReviseInFlightGeometry` (paced_wire.go) runs on the EDGE MOVER's goroutine and computes

    t = (nowTick - b.placementTick) / (b.arc / pulseSpeed)

where `placementTick` was written by the SOURCE node's goroutine. Two clocks, subtracted.

**Resolution: a wire keeps ONE clock.** `pw.clock` stays a single object shared by the
three goroutines that touch that wire (source placing/stepping, destination polling, edge
mover revising). Then the subtraction above is within one clock and the offset cannot
appear. Per-goroutine is the wrong granularity; **per-wire** is the boundary that matters,
because a wire is the only place two goroutines' ticks are compared.

Everything else — node loop pacing, drive placement cadence, sleep — is self-contained
within one goroutine and needs no agreement with anyone.

## Delivery — the real open question

This, not timing, is the hard part.

A speed change must reach every clock. It must NOT be droppable: a dropped transition
means that goroutine runs at the wrong rate indefinitely, with no event to correct it.
That is not the same class as a stale value that self-heals. `sendMoveLossy` discarded
9345 of 9600 neighbour messages this session under a comment claiming drops were safe —
do not reuse a lossy path here and do not add one.

Two unresolved shape questions:

1. **`DriveHeld` goroutines have no inbox.** They are spawned per driven output and hold
   only an `Out`. Reaching them needs a new channel per drive goroutine, or the clock
   being a small value they re-read from somewhere they already touch.
2. **Every paced loop grows a poll.** Look at `input/node.go`'s loop: it polls exactly one
   channel (`FeedbackIn`) today. Each loop like it needs a second thing to check. That is
   a change to the shape of every node kind, and it is the real cost of this work —
   independent of whether the timing story is easy.

Settle both before writing code.

## What must be proven

1. **Per-wire agreement holds.** A bead placed by the source goroutine and measured by the
   edge mover during a geometry rebase must land at the same `t` as today. This is the one
   place the split could bite, so it is the one test that must exist.
2. **Delivery is not lossy.** A goroutine that is asleep when a transition is sent must
   still apply it on waking. Prove by sending during a sleep window and asserting the rate
   changes.
3. **No goroutine is left behind.** Every clock-holder receives every transition — assert
   over the full set, not a sample. This guards the failure mode that is impossible today.
4. **The mutex is genuinely gone.** `RealClock.mu` deleted; `clock_concurrency_test.go`'s
   race tests deleted or rewritten. They exist to prove a lock is load-bearing, and a test
   that cannot fail must not outlive the lock it guarded.
5. `-race` clean at `-count=5`, plus the drag and persistence suites.

## What this buys

- Deletes the widest-fan-in mutex in the system.
- The clock stops being an exception to the rule that nodes own their own state.

## What it costs

- Every paced loop grows a poll (see Delivery).
- A failure mode that cannot exist today: a clock that never hears a transition runs at
  the wrong rate forever. Item 3 is the guard.
- Up to ~16 ms of stagger on pause, and 1-2% per-wire offset per speed change — both
  accepted under the premise.

## Not doing

Not replacing the shared object with an `atomic.Pointer` snapshot. It would also remove
the lock and is a smaller change, but it keeps one shared clock, which is what is being
removed. Recorded so it is not re-proposed as an improvement.
