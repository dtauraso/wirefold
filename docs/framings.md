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
| `outbox.mu` + `cond` | `node_mover.go:57-58` | the unbounded move queue | Contention **verified** (bypassing it reproduces the cascade deadlock). Restructuring **unexamined** — it is SPSC, a shape with known lock-free forms. Also UNCHECKED: nothing bounds queue growth or drain time |
| `Trace.mu` | `Trace/Trace.go:294` | `events`/`closed`/sinks | **Staying.** Cannot be copied — every goroutine's events must land in one place to become one buffer; copies give N partial streams. The *ordering* framing above it is still open (`task/per-owner-buffer-rows`) |
| `LayoutHolder.mu` | `layout_holder.go:118` | `localPolars`/`pole` | **Examined, UNCONTENDED.** Kept as cheap insurance against a future cross-goroutine `md.layoutHolders[m]` call, NOT because it guards a present race. Do not cite it as evidence that cross-goroutine holder access is expected |
| `sceneFileMu`, `debouncedPersister.mu`, `entityFileMuMu` (+ per-path `entityFileMus`) | `scene_persist.go:44, 58, 168-177` | read-modify-write cycles on the runtime scene JSON and per-entity files, plus debounce state | **Never examined.** These serialize FILE I/O across genuinely distinct writers (camera, overlays, polar locks) — a different problem from the in-memory framings above, since the observer is the filesystem and it has no perceptual threshold |

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

That is three for three. **Treat "no restructuring removes this lock" as unproven whenever
you meet it here**, including in the one remaining row below.

**`outbox.mu` is still unexamined.** Its contention is real — bypassing the outbox
reproduces the cascade deadlock as a timeout, red-proven and guarded by tests. But the
restructuring question is open, and there is a specific reason to think it is live:

- `outbox` is **single-producer, single-consumer** — the mover's own handler enqueues, one
  dedicated sender drains. SPSC is precisely the shape with well-known lock-free
  implementations. The unbounded requirement complicates it; it does not obviously rule it
  out.

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
because giving the wire its own goroutine made ownership structural. Both were once
described as staying. `outbox.mu` is the last one still described that way.

## The test to apply next time

Before defending an exactness, ask **who could observe the difference, and at what
resolution**. Not "does this guarantee hold" — under real connection and real time it
never exactly does. The useful question is whether anyone can tell.

For this system the observers are: an eye at 1/60 s, and the memory model. The first has a
threshold measured in ticks and pixels. The second has none — it is exact or it is broken.
Nearly every framing in the table above was applying the second standard to the first
observer.
