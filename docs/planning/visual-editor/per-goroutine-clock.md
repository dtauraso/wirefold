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
wire — reached through `In.Clock()` / `Out.Clock()` and `reflectBuild`. Pacing loops call
`Tick()` continuously; `stdin_reader.go` calls `SetSpeed` occasionally. Its mutex exists
solely because those two collide — the widest-fan-in lock in the system.

Precisely: the contended surface is **`Tick()` alone**. `SleepCycle` is a bare
`time.After(tickPeriod)` and never takes the lock, so the lock is not on the per-cycle
sleep path — it is on every `Tick()` read, which pacing loops do continuously.

New model: **every goroutine holds its own clock, reading the system monotonic clock
directly.** The clock is an object INSIDE the goroutine — not a field on a shared struct
the goroutine reaches through, which is the same shared-object model wearing a different
name. Nothing shared, nothing to lock.

The mutex is a few lines. The surface built on the assumption of one injected shared
object is most of the work — see **API demolition** below, which is the real size of this
change.

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

Still to settle. This is plumbing rather than semantics, but it is not small:

- `DriveHeld` goroutines have no inbox — spawned per driven output, holding only an `Out`.
  Today they get a clock from that `Out` (`drive.go:56`, `clk := out.Clock()`), and it is
  tempting to read that as "the `Out` is already the delivery path, so no inbox is needed."
  It is not. Hanging the copy off the `Out` puts it back on a shared struct several
  goroutines reach through — the shared-object model renamed. Once the clock is inside the
  goroutine, a received message is the ONLY way in, which is exactly why the inbox is
  required. Recorded because this wrong turn was taken once.
- Every paced loop grows a second thing to check. `input/node.go` polls exactly one channel
  (`FeedbackIn`) today. `DriveHeld`'s only blocking point is its `sleep`, so the inbox
  joins that select.

A dropped change is still worse than a late one — that copy runs at the wrong rate with no
event to correct it — so the path must not be lossy. But this is no longer a correctness
cliff needing its own mechanism.

## API demolition

The plan above settles the semantics and understates the edit. The mutex is a few lines;
the surface built ON the assumption of one injected shared object is most of the work.
Everything here was read out of the code, not inferred.

**The clock is an object INSIDE the goroutine.** Not a field on a shared struct the
goroutine reaches through — that is the same shared-object model wearing a different name.
This is the line that decides every item below.

### 1. Port accessors go away

`In.Clock()` and `Out.Clock()` (ports.go) are how goroutines fetch the shared clock today —
`drive.go:56` is `clk := out.Clock()`. A goroutine that constructs its own clock does not
ask a port for one. Both accessors, and `PortBindings.clock` / `PacedWire.clock` behind
them, are removed rather than rewired.

### 2. `Out.Paced()` is a clock-existence test — this is the trap

    func (o *Out) Paced() bool { return o != nil && o.pw != nil && o.pw.clock != nil }

The paced-vs-chan MODE predicate is `a clock exists on the wire`. `DriveHeld` and
`gatecommon.RunGate` branch on it to choose clock pacing vs. wall-clock pacing. Delete
`pw.clock` naively and every paced wire silently reports chan mode — no compile error, the
network just stops being clock-paced. **Mode must be re-encoded on something that is not a
clock before `pw.clock` is touched.** Nothing else in this demolition can fail this quietly.

### 3. `inertClock` deletes itself

`inertClock` / `NewInertClock` exist ONLY because an injected clock can be absent: an
unwired `In` needs a non-nil thing to return, and `reflectBuild` matches
`input.Node.Clock` by exact type, so a rename silently injects nothing and the first
unguarded `clk.Tick()` panics with no recover over the node goroutine. A goroutine that
constructs its own clock cannot have a nil one. The whole fallback path, its two doc
comments, and `PortBindings.inClock()` go — an unrepresentable-nil trap removed, not
relocated. Claim it as a win of this change.

### 4. `SetSpeed` leaves the interface

`Clock` today is `Tick / SleepCycle / SetSpeed`. In the new model nothing outside the
goroutine can call `SetSpeed` — the transition arrives as a received message and the
goroutine applies it to its own object. The interface LOSES a method. `stdin_reader.go:322`
(`clk.SetSpeed(...)`) becomes a send, not a call.

Note `SleepCycle` never touched `mu` — it is a bare `time.After(tickPeriod)`, wall time
regardless of speed. So the contended surface is `Tick()` alone, and the "lock hit by every
goroutine every cycle" framing above is about `Tick`, not all clock traffic.

### 5. `reflectBuild` injection

`builders.go` injects the shared clock three ways: `EmitRefillSlide`, `Tick`, and a bare
`Wiring.Clock` field. All three are the shared object arriving from outside. They become
construction inside the goroutine, and the type-matched injection (item 3's rename hazard)
goes with them.

### 6. `Buffer/snapshot.go` is NOT a node goroutine

`SetTickSource(func() int64)` injects one tick reader to coalesce the high-volume
`KindPosition` stream to one emit per tick. This is a consumer outside the node set, and
"every goroutine holds its own" does not answer it. It needs its own copy like anything
else — but it is the one clock whose reading the EDITOR sees directly, so decide it
deliberately rather than by default.

### 7. Test surface

Item 4 of "What must be proven" names `clock_concurrency_test.go` for deletion. Also
present: `clock_speed_test.go`, `clock_realclock_test.go`, and ~20 files constructing
clocks via the port/loader path. Most do not die — they move to the new construction shape.

### Order

Item 2 first (re-encode mode off clock-existence), then the accessors, then `pw.clock` and
`PortBindings.clock`, then `inertClock`, then the interface. Deleting `mu` is last and is
the smallest edit in the list.

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
   that cannot fail must not outlive the lock it guarded. `clock_speed_test.go` and
   `clock_realclock_test.go` move to the new construction shape rather than dying.
5. **Paced mode survives the demolition.** `Out.Paced()` is a clock-existence test today
   (API demolition item 2), so a paced wire silently falling back to chan mode is a
   COMPILE-CLEAN failure. Assert paced wires still report paced after mode is re-encoded —
   this is the one step whose breakage the compiler will not catch.
6. `-race` clean at `-count=5`, plus the drag and persistence suites.

## What this buys

- Deletes RealClock.mu outright — the widest-fan-in lock in the system, taken on every
  `Tick()` read by every pacing loop. Not reduced: removed.
- The clock stops being an exception to the rule that nodes own their own state.
- Deletes an unrepresentable-nil trap: `inertClock` and the `reflectBuild` type-matched
  injection exist only because an injected clock can be absent (API demolition item 3). A
  goroutine that constructs its own clock cannot have a nil one, so the whole fallback path
  and its silent-rename panic go with it.

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
