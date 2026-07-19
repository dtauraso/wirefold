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

No "effective at" timestamp on transitions, no scheduling. An earlier draft of this plan
built all of that to force every copy to bank at an identical instant; it is unnecessary
under the premise, and it created a worse problem than it solved (retroactive correction
sends a clock backward over ticks it already acted on, which the monotonicity test
forbids). Do not reintroduce it.

### The residual, sized — and why it is not a floor

Banking at local `now` means copies acquire a permanent offset per speed change:

    offset = (delivery skew) x (new speed - old speed)   ~= 1 tick per change

Against this topology (edges 57-268 wu, a crossing is tens of ticks) that is **1-2% of a
wire per speed change**, and offsets are signed by direction so speeding up and slowing
down partly cancel.

There is exactly one place two copies are subtracted from each other —
`ReviseInFlightGeometry` (paced_wire.go), which runs on the edge mover's goroutine and
computes

    t = (nowTick - b.placementTick) / (b.arc / pulseSpeed)

where `placementTick` was stamped from the source node's clock.

An earlier draft called this a FLOOR: it argued a wire must keep one shared clock, which
made the smallest shareable group a node plus its drive goroutines plus the edge movers of
its out-wires — 3-5 goroutines, still shared, still a mutex, and the whole point lost.

**That was wrong.** The error at that subtraction IS the same offset: delta of ~1 tick over
a crossing of tens of ticks, i.e. the same 1-2%. Having accepted that magnitude everywhere
else, there is no basis for rejecting it here. The floor was exactness re-imported after it
had already been dropped.

So: **every clock user holds its own copy. Nothing is shared. `RealClock.mu` is deleted,
not reduced.**

## Delivery

A speed change is now just a value each copy applies when it hears it. A copy that hears
late is off by the amount already accepted above, so late delivery needs no special
handling and no timestamp.

Still to settle, and it is plumbing rather than semantics:

- `DriveHeld` goroutines have no inbox — spawned per driven output, holding only an `Out`.
- Every paced loop grows a second thing to check. `input/node.go` polls exactly one channel
  (`FeedbackIn`) today.

A dropped change is still worse than a late one — that copy runs at the wrong rate with no
event to correct it — so the path must not be lossy. But this is no longer a correctness
cliff needing its own mechanism.

## Trace.mu is NOT affected

`Trace` cannot be copied. It is an accumulator: every event from every goroutine has to
land in one place to become one buffer for the editor. Copies would produce N partial
event streams and no whole picture. That mutex stays, and this plan does not touch it.

## What must be proven

1. **The rebase stays within tolerance.** A bead placed by the source goroutine and
   measured by the edge mover during a geometry rebase must land within the accepted
   1-2%-per-speed-change band — not identical to today, but visibly indistinguishable.
   Assert the bound explicitly so the accepted magnitude is written down in code.
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

- Deletes RealClock.mu outright — the widest-fan-in lock in the system, hit by ~16
  goroutines on every cycle. Not reduced: removed.
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
