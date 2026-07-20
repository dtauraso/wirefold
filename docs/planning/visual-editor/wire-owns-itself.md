---
branch: task/mutex-pacedwire-restructure
---

# The wire owns itself

## The change, in one line

`PacedWire` stops being a passive struct that three goroutines lock, and becomes an
**active goroutine that owns its own beads**, with a channel on each end.

    TODAY   source node ──lock──▶ [ PacedWire ] ◀──lock── dest node
                                        ▲
                                        └──lock── edgeMover.run (per edge)

    THIS    source node ──chan──▶ [ wire goroutine ] ──chan──▶ dest node
                                   owns inflight/delivered
                                   owns its own geometry

The edge mover is not a separate actor beside the wire. **It IS the wire's goroutine.**

## Why this answers the open row

`docs/framings.md` records `PacedWire.mu` as *contention verified, restructuring
UNEXAMINED*. This is the restructuring. It was never examined; here it is.

The lock exists because three independently scheduled goroutines mutate one wire object:

| Today's contender | What it does | Becomes |
|---|---|---|
| source node's drive loop | `placeBeadNoWalkerAt` :188, `StepOnceAt` :280-328 | a send on the wire's in-channel |
| destination node's Update | `PollRecvTick` :365 | a receive on the wire's out-channel |
| `edgeMover.run` (per edge) | `ReviseInFlightGeometry` :389 | **the wire goroutine itself** |

With one owner, `inflight`/`delivered` are touched by exactly one goroutine.
**`PacedWire.mu` is deleted, not reduced** — the same move that removed `RealClock.mu`:
ownership replaces locking.

## It costs no new goroutines

This is the part that makes it cheap. `edgeMover.run` **already exists, one per edge**
(spawned `node_move.go:506`). The wire goroutine is not an addition — it is that goroutine,
given the beads it was already reaching across a lock to revise.

Goroutine count is unchanged. What changes is that the edge's goroutine stops being a
visitor to shared state and becomes its owner.

## What this contradicts, and must be updated with it

MODEL.md pins the opposite, in two places, and both must change **in the same commit**:

- §The network: *"A wire (`PacedWire`) is NOT a goroutine or a channel — it is a passive,
  mutex-guarded struct (`inflight`/`delivered` bead slices) that the owning node's
  goroutine STEPS via `StepOnceAt`."*
- §What things are, Wire: *"A passive mutex-guarded struct, not a goroutine or channel: the
  source node calls `placeBeadNoWalkerAt` directly … the owning node's `StepOnceAt` times
  the traversal."*

Do not leave those standing. A model doc that contradicts the code is worse than no model
doc — the whole point of MODEL.md is that it is the thing you re-derive from.

Note what does NOT change: **beads still travel a straight line**, timed by
`ticksToCross = arcLength / pulseSpeed`, each carrying its own placement geometry. The
rejected bead-item CHAIN model (each bead following its predecessor, O(N²) follow latency)
stays rejected — this changes who owns the slice, not how a bead is timed or drawn.

## Open questions to settle before building

1. **Does the wire goroutine step on its own clock copy?** It would own one, like every
   other goroutine (per-goroutine clock is already shipped). Then `StepOnceAt`'s caller-pinned
   tick — which exists precisely so several wires observe the SAME tick rather than each
   re-reading a live clock — has no caller to pin it. Either the wire reads its own tick
   (simpler, and consistent with the clock model) or the tick still arrives on the channel
   with the bead. **This is the one real design decision.**
2. **Backpressure.** A channel has a buffer; a slice does not. If the in-channel fills,
   does the source block? MODEL.md §Sending is explicit that a node "does not check the
   wire's state and does not wait on the destination — there is no back-pressure." A
   blocking send would violate that. Buffered + non-blocking, or the send must not be able
   to fail.
3. **Geometry rebase during a drag.** `ReviseInFlightGeometry` currently runs on the edge
   mover while the source may be placing. Once both are the same goroutine this is
   sequential and the race disappears — but confirm the drag path does not need to revise
   geometry *synchronously* from the gesture FSM's goroutine.
4. **Teardown.** `teardownGen` exists to invalidate in-flight beads when a wire is rebuilt.
   Channel close semantics have to cover what that generation counter covers.

## What must be proven

1. `PacedWire.mu` is gone — deleted, not narrowed.
2. `-race -count=3 ./...` clean, since ownership is now the only thing preventing the race
   the lock used to prevent.
3. Bead timing is visually unchanged: `ticksToCross` still governs, and a drag still rides
   beads at constant fractional `t`.
4. No new goroutines: assert the count is the same as before (edge movers reused, not added).
5. Drive it in the LIVE editor. Green suites hid two speed bugs on the clock work; the
   editor found both.
