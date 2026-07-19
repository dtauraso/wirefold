---
branch: task/mutex-shared-services
---

# Per-goroutine clock

## The change

Today there is ONE `*RealClock`, constructed in `main.go` and injected into every node,
port and wire. ~16 goroutines call `Tick()` on it continuously; `stdin_reader.go` calls
`SetSpeed` on it occasionally. Its mutex exists solely because those two collide.

The new model: **every goroutine holds its own clock object**, and each one reads the
system monotonic clock directly. Nothing is shared, so nothing needs a lock.

The system clock is already global, already consistent across goroutines, and already
safe to read concurrently. It is the shared timeline. There is no reason to wrap it in a
second shared object and then guard that object.

## Why the timeline still agrees

`Tick()` is a pure function of three things: an origin, the speed history, and `now`.

    tick(now) = (accScaled + (now - lastChange) * speed) / tickPeriod

If two clocks start from the same origin and apply the same speed history, they return
identical values for the same `now` — no communication required. They agree because they
compute the same function, not because they consult the same memory.

That is what makes this work at all. Copies of mutable state drift; copies of a pure
function do not.

## The one real problem: speed changes

This is the whole design risk, and it is not the distribution mechanism — it is WHEN
each copy applies the change.

`SetSpeed` today banks the elapsed segment at `time.Now()`:

    accScaled += (now - lastChange) * speed
    lastChange = now
    speed = newSpeed

If each local clock ran that on receipt, every clock would bank at a slightly different
instant. That error does not wash out — it is added to `accScaled` and carried forever.
Sixteen clocks would each acquire their own permanent offset, and beads on different
wires would fall progressively out of step. A drift bug of this shape would be very hard
to see and very hard to attribute.

**Fix: the transition carries its own wall instant.** A speed change is distributed as
`{speed, effectiveAt}`, and a local clock banks using `effectiveAt`, never `time.Now()`:

    accScaled += (effectiveAt - lastChange) * speed
    lastChange = effectiveAt
    speed = newSpeed

Now it does not matter when a goroutine hears about the change. Applying it late produces
exactly the same state as applying it on time. Ordering and latency stop mattering, which
is what makes this safe to do without coordination.

## OPEN FLAW — effectiveAt conflicts with monotonicity

Found by asking what a PAUSE looks like. The plan above is not yet sound.

Today `SetSpeed(0)` is one write to one object, so the tick stops advancing instantly and
every goroutine's next read — whenever it wakes — sees it frozen. Pause is atomic in
tick-space even though goroutines wake staggered.

Per-goroutine it is not. A goroutine that has not yet received the transition keeps
computing at the old speed and keeps stepping beads. Then applying `{speed, effectiveAt}`
retroactively banks the segment as of T — and its tick goes BACKWARD:

    pause at T=100, goroutine hears at 110
    at t=105 it computed tick 105 and advanced a bead on it
    at t=110 it banks accScaled=100, speed=0  ->  tick now reads 100

It already acted on ticks the corrected clock says never happened.
`TestRealClockConcurrentMonotonic` asserts exactly that this never occurs.

So the two properties cannot both hold:

  - CLAMP (never go backward): the overshoot is permanent and clocks disagree by exactly
    the quantity effectiveAt was introduced to eliminate.
  - CORRECT (apply as of T): clocks agree, but ticks run backward and work has already
    been done on un-issued ticks.

The claim above that late delivery is "indistinguishable from applying it on time" is
true of the arithmetic and FALSE of the observable behaviour of a goroutine that already
acted during the window. Do not build on it until this is resolved.

Directions worth considering, none verified:
  - Make a speed change take effect at a FUTURE tick rather than a past instant, chosen
    far enough ahead that every loop will have woken — turns a retroactive correction
    into a scheduled one, at the cost of pause not being immediate.
  - Accept bounded overshoot and drop the exact-agreement requirement, if beads being up
    to one cycle out of step is acceptable visually.
  - Keep one shared clock. The mutex is cheap; this problem is not.

## Delivery

Whatever carries the transition MUST NOT DROP IT. A dropped transition is permanent
divergence for that goroutine — not a stale value that self-heals on the next event.

This is the same trap as `sendMoveLossy` earlier this session, where 9345 of 9600
neighbour messages were being discarded under a comment claiming the drop was safe. Do
not reuse a lossy path here, and do not add one.

Open: `nodeMover`s have inboxes, but `DriveHeld` goroutines do not — they are spawned per
driven output and only hold an `Out`. Reaching them needs either a new channel per drive
goroutine, or the clock handed to them being a small value they re-read. Decide this
before writing code; it is the part most likely to force a shape change.

## What must be proven

1. **No drift.** N independent clocks, constructed at one origin, driven through a
   sequence of speed changes at randomised delivery latencies, must return IDENTICAL
   ticks for the same `now`. Test this directly with an injectable `now` rather than
   wall time, so it is deterministic. This is the test that would have caught the
   bank-at-receipt bug.
2. **Late delivery is indistinguishable.** Apply the same transition immediately to one
   clock and 500ms late to another; assert both agree afterwards.
3. **Cross-goroutine tick comparisons still hold.** A bead's placement tick is recorded
   by one goroutine and evaluated later; the buffer coalesces per tick. Prove a wire's
   crossing decision is unchanged.
4. **The mutex is genuinely gone.** `RealClock.mu` deleted, and
   `clock_concurrency_test.go`'s existing race tests either deleted or rewritten — they
   exist to prove a lock is load-bearing, and a test that cannot fail must not survive
   the lock it guarded.
5. `-race` clean at `-count=5`, plus the drag and persistence suites.

## What this buys

- Deletes the widest-fan-in mutex in the system — ~16 goroutines currently serialise on it
  on every cycle, forever.
- Removes a shared object from a model whose whole claim is that nodes own their own
  state and coordinate by message.
- The clock stops being an exception to that rule.

## What it costs

- Speed changes become a distributed fact rather than a single write. That is strictly
  more machinery than `c.speed = x`.
- A new failure mode that does not exist today: a goroutine whose clock never learns of a
  transition runs at the wrong rate indefinitely. Today that is impossible, because there
  is one object. Item 1 above is the guard against it.

## Not doing

Not replacing the shared object with an `atomic.Pointer` snapshot. That would also remove
the lock and would be a smaller change, but it keeps one shared clock — which is the thing
being removed. Recording it here only so it is not re-proposed as an improvement.
