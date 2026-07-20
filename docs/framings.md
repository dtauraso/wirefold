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
| A holder is written only by its owner | Neighbours reach into each other, so holders need guarding | `LayoutHolder.mu`, and two doc comments naming the wrong goroutines | **Done** — contention refuted, comment corrected, no vacuous test shipped |
| Budget is pixels of relative displacement during motion | Every block of a frame must describe the same instant | `SnapshotState`'s accumulate-then-pack, the drain merge | Planned — `task/per-owner-buffer-rows` |
| Each owner's own event order is the only real one | Events have a global total order | `Trace.mu`, the event channel, the drain's ordering | Planned — same branch. Note the merged log is already only *arrival order at the drain*, which is scheduler order, not causal order |
| Pack at the rate the consumer consumes | Emission must be driven by change | `emitSnapshot` scattered across `Update`'s arms, tick coalescing | Planned — same branch |

## Every mutex left in the tree

The rows above track *framings*. This tracks *locks* — every `sync.Mutex`/`Cond` in
non-test Go, so "which are left" is answerable without grepping. Verified against the code
when written; re-grep before trusting it.

| Lock | Where | What it guards | Status |
|---|---|---|---|
| ~~`PacedWire.mu`~~ | — | was `inflight`/`delivered` | **DELETED.** The wire became its own goroutine, so it has one owner and nothing to guard — see below |
| ~~`outbox.mu` + `cond`~~ | — | was the unbounded move queue | **DELETED.** Per-direction channels replaced the shared queue; with no blocking send there is nothing to hold — see below |
| `Trace.mu` | `Trace/Trace.go:294` | `events`/`closed`/sinks | **Staying.** Cannot be copied — every goroutine's events must land in one place to become one buffer; copies give N partial streams. The *ordering* framing above it is still open (`task/per-owner-buffer-rows`) |
| `LayoutHolder.mu` | `layout_holder.go:118` | `localPolars`/`pole` | **Examined, UNCONTENDED.** Kept as cheap insurance against a future cross-goroutine `md.layoutHolders[m]` call, NOT because it guards a present race. Do not cite it as evidence that cross-goroutine holder access is expected |
| ~~`sceneFileMu`, `entityFileMuMu`, per-path `entityFileMus`~~ | — | were read-modify-write cycles on shared JSON files | **DELETED.** Every one existed because two writers shared one file; the files were split so each writer owns its own — see below |
| `debouncedPersister.mu` ×5 | `scene_persist.go:64` | `pending` / `has` / `timer` / `writes` | **The last unexamined lock.** Splitting files did not touch it: this is IN-MEMORY debounce state shared between the goroutine that arms the timer and the `time.AfterFunc` goroutine that fires it. Five independent instances |

`RealClock.mu` was the widest-fan-in lock and is **gone** — deleted by
`task/mutex-shared-services`, replaced by ownership (one clock copy per goroutine) rather
than by a smaller lock. Its two rows left this table with it.

Worth carrying into the rows above: it was never removed for speed. Measured contention was
~500-1700 acquisitions/sec against a mutex that does tens of millions, and no benchmark or
profile in this repo ever motivated it. It went because a shared object forces every reader
of the code to understand the whole network's clock wiring before understanding one
goroutine, and because the lock defended an exactness — every reader agrees to the
millisecond — that nothing here needs. "Widest fan-in" was a structural claim being read as
a cost claim. Check which kind you are making before pulling on any remaining lock.

## `PacedWire.mu` was examined, and the answer was yes

For a while this section said the pair `outbox.mu` and `PacedWire.mu` were unsettled: their
contention was red-proven, but the claim that *no restructuring makes the sharing go away*
was asserted and never examined — the same claim that had already been wrong about `geomMu`
and about `LayoutHolder.mu`.

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

**That is four for four.** `geomMu`, `LayoutHolder.mu`, `PacedWire.mu`, `outbox.mu` — every
lock this table ever described as necessary has been removed by a restructuring, and in
every case the restructuring was *giving the state one owner*, not being clever about
locking. **Treat "no restructuring removes this lock" as unproven wherever you meet it**,
including in the rows that remain. The question to ask is never "is this lock correct" but
"who could own this outright".

What is left is genuinely different in kind: `Trace.mu` guards an accumulator that cannot be
copied without producing N partial streams; `LayoutHolder.mu` is already single-owner and
uncontended; `scene_persist`'s four serialize file I/O, where the observer is the filesystem
and has no perceptual threshold. None of those is "a lock nobody has thought about".

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
because removing blocking sends left its queue with nothing to hold. All three were once
described as staying, as was `LayoutHolder.mu` before them.

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
