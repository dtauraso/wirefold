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
| Skew is ~1 tick; the observer samples at 1/60 s | Every clock must agree exactly | One shared `RealClock`, `RealClock.mu`, and a proposed timestamp-on-transition scheme | Planned — `task/mutex-shared-services` |
| Stopping within a tick is imperceptible | Pause must take effect simultaneously everywhere | The same shared clock; any barrier or coordinator that would enforce simultaneity | Planned — same branch |
| Budget is pixels of relative displacement during motion | Every block of a frame must describe the same instant | `SnapshotState`'s accumulate-then-pack, the drain merge | Planned — `task/per-owner-buffer-rows` |
| Each owner's own event order is the only real one | Events have a global total order | `Trace.mu`, the event channel, the drain's ordering | Planned — same branch. Note the merged log is already only *arrival order at the drain*, which is scheduler order, not causal order |
| Pack at the rate the consumer consumes | Emission must be driven by change | `emitSnapshot` scattered across `Update`'s arms, tick coalescing | Planned — same branch |
| A queue's invariant genuinely must be maintained | — | `outbox.mu` + cond, `PacedWire.mu` | **Confirmed, staying** — no restructuring makes these fall out for free |

## Why the last row is different

Not every guarantee is a mistake. `outbox.mu` and `PacedWire.mu` guard state that several
goroutines genuinely mutate, and no split makes the property structural. A torn slice
header is wrong at any resolution — there is no observer threshold below which corrupted
memory is acceptable.

The distinction is not global-vs-local. It is:

- **Maintained** guarantees — something must keep doing work to hold them true. These
  break under real conditions, because holding them requires everyone present and prompt.
- **Structural** guarantees — they fall out of construction. Immutability. A pure
  function. These cost nothing and do not break.

`geomMu` went away because a split made the property structural. `PacedWire.mu` stays
because a queue's invariant has to be maintained.

## The test to apply next time

Before defending an exactness, ask **who could observe the difference, and at what
resolution**. Not "does this guarantee hold" — under real connection and real time it
never exactly does. The useful question is whether anyone can tell.

For this system the observers are: an eye at 1/60 s, and the memory model. The first has a
threshold measured in ticks and pixels. The second has none — it is exact or it is broken.
Nearly every framing in the table above was applying the second standard to the first
observer.
