# Framings — what replaced what, and the architecture built for the old one

Each row is a framing that turned out to be wrong for this system, the framing that
replaced it, and the machinery that exists *because of* the old one. That last column is
the point: a wrong framing does not just mislead, it leaves structure behind.

The pattern in every row is the same. The replaced framing is a **global guarantee** —
statable without knowing anything about wirefold, assuming a universe with no propagation
delay and one universal now. The correct framing is a **threshold against an actual
observer**, which requires knowing what this system is and who is looking at it.

| Correct framing | Framing it replaced | Architecture built for the replaced framing | Status |
|---|---|---|---|
| Identity is write-once, so tearing is unrepresentable | A shared struct must be guarded against concurrent read | `geomMu`, plus a widened read invented to make the guard falsifiable | **Done** — `nodeGeom` split into `nodeIdentity` + mutable state, lock deleted |
| A holder is written only by its owner | Neighbours reach into each other, so holders need guarding | `LayoutHolder.mu` | **Done** — every non-test caller confirmed to run on the owning node's own goroutine; the lock was deleted, not narrowed; the nine tests that had polled live state cross-goroutine were fixed to wait on a happens-before edge instead |
| Budget is pixels of relative displacement during motion | Every block of a frame must describe the same instant | `SnapshotState`'s accumulate-then-pack, the drain merge | Planned — `task/per-owner-buffer-rows` |
| Each owner's own event order is the only real one | Events have a global total order | `Trace.mu` (deleted), the event channel, the drain's ordering | Planned — `task/framing-event-order`, `task/per-owner-buffer-rows`. Note the merged log is already only *arrival order at the drain*, which is scheduler order, not causal order. Deleting `Trace.mu` did not close this row: the event channel and the drain's single-writer ordering still exist, and whether that ordering should be treated as authoritative is still open |
| Pack at the rate the consumer consumes | Emission must be driven by change | `emitSnapshot` scattered across `Update`'s arms, tick coalescing | Planned — same branch |

## Not one lock is left

Every `sync.Mutex` and `sync.Cond` in non-test Go has been removed. This section used to be
an inventory of the ones that remained; there is nothing to inventory, so it is gone rather
than kept as an empty table promising a lookup it cannot answer.

The one measured lesson worth carrying forward, from `RealClock.mu` — the widest-fan-in of
them: it was never removed for speed. Contention measured ~500-1700 acquisitions/sec against
a mutex that handles tens of millions, and no benchmark or profile in this repo ever
motivated it. It went because a shared object forces every reader of the code to understand
the whole network's clock wiring before understanding one goroutine, and because the lock
defended an exactness — every reader agrees to the millisecond — that nothing here needs.
"Widest fan-in" was a structural claim being read as a cost claim. Check which kind you are
making before pulling on any shared state.


## `PacedWire.mu` was examined, and the answer was yes

For a while this section said the pair `outbox.mu` and `PacedWire.mu` were unsettled: their
contention was red-proven, but the claim that *no restructuring makes the sharing go away*
was asserted and never examined — the same claim that had already been wrong about `geomMu`.

**`PacedWire.mu` has now been examined, and the assertion was wrong a third time.** The
question was whether `inflight`/`delivered` separate by owner. They do — but not by
splitting the pair. The wire itself became the owner: `PacedWire` is now an active goroutine
with a channel on each end, and the per-edge `edgeMover.run` that used to reach across the
lock to revise geometry IS that goroutine. One owner, nothing to guard, lock deleted. It
cost no new goroutines, because that edge goroutine already existed.

**`outbox.mu` went the same way, immediately after.** Its contention was real — bypassing
the outbox reproduced the cascade deadlock — but the deadlock had exactly one cause:
`handle` blocked while sending, so it never returned to drain, so its peer (blocked sending
into it) never returned either. Movers now have **two directed channels per pair** (A→B and
B→A, no shared inbox), a non-blocking send that **retains and retries** rather than blocking
or dropping, and a loop paced on the human-speed clock like every other loop in the system.
With no blocked senders there is nothing for a queue to hold, so the queue, its mutex, its
cond, its dedicated sender goroutine and that goroutine's ctx-watcher are all gone —
goroutine count went **down** by two per mover.

**That is seven for seven.** `geomMu`, `RealClock.mu`, `PacedWire.mu`, `outbox.mu`,
`LayoutHolder.mu`, `debouncedPersister.mu`, `Trace.mu` — every lock this doc ever called
necessary has been removed by a restructuring, and in every case the restructuring was
*giving the state one owner*, not being clever about locking. `Trace.mu` looked like the
strongest holdout — "an accumulator that cannot be copied without producing N partial
streams" — and that claim was even true. It just wasn't what the lock was doing: in
production nothing ever raced `events` (the drain was already its sole writer, `Events()`
had zero non-test callers, `WriteJSONL` ran after the drain exited). The lock's actual job
was serializing `Breadcrumb`'s direct sink writes and surviving a shutdown that didn't wait
for every goroutine. Fixing those two things — not narrowing or re-justifying the
accumulator claim — removed it. **Treat "no restructuring removes this lock" as unproven
wherever you meet it, and treat a correct-sounding justification for a lock as no guarantee
it describes what the lock actually guards.** The question to ask is never "is this lock
correct" but "who could own this outright".

What was left is now gone too, by the same move: nothing in non-test Go holds a
`sync.Mutex` or `sync.Cond`.

What IS categorical, and does not depend on any of the above: a torn slice header is wrong
at any resolution. There is no observer threshold below which corrupted memory is
acceptable. So whatever replaces these must still be memory-safe — but that is a
constraint on the replacement, not a reason there cannot be one.

The distinction that sorts the other rows is not global-vs-local. It is:

- **Maintained** guarantees — something must keep doing work to hold them true. These
  break under real conditions, because holding them requires everyone present and prompt.
- **Structural** guarantees — they fall out of construction. Immutability. A pure
  function. These cost nothing and do not break.

`geomMu` went away because a split made the property structural. `PacedWire.mu` went away
because giving the wire its own goroutine made ownership structural. `outbox.mu` went away
because removing blocking sends left its queue with nothing to hold. `LayoutHolder.mu` went
away because every non-test caller was already confined to its own node's goroutine — the
lock was residue from a since-deleted second goroutine, not insurance against a live race.
All four were once described as staying.

Every one of those was a **maintained** guarantee wearing the costume of a necessary one.
The tell is the same each time: the lock is defending an invariant across goroutines that
did not need to share in the first place.

## The test to apply next time

Before defending an exactness, ask **who could observe the difference, and at what
resolution**. Not "does this guarantee hold" — under real connection and real time it
never exactly does. The useful question is whether anyone can tell.

For this system the observers are: an eye at 1/60 s, and the memory model. The first has a
threshold measured in ticks and pixels. The second has none — it is exact or it is broken.
Nearly every framing in the table above was applying the second standard to the first
observer.
