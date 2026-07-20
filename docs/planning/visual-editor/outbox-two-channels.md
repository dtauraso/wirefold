---
branch: task/mutex-outbox-restructure
---

# Two channels, no inbox, no blocking

## The design

Between any two movers that talk, there are **two directed channels** — `A→B` and `B→A`.
Not one shared inbox per mover. Not a queue guarded by a lock.

Each mover's loop **starts by reading its incoming channel, non-blockingly**, and decides
what to do with the result if there was one. Then it does its own work. Then it loops.

    for {
        if msg, ok := <-in (non-blocking); ok {
            handle(msg)
        }
        ... own work ...
    }

Nothing blocks. Not the read, not the send.

## Why this kills the deadlock

The cascade deadlock had exactly one cause: **`handle` blocked while sending**, so it never
returned to drain its own inbox, so its peer — blocked sending into *it* — never returned
either. A cycle of two goroutines each waiting on the other.

Remove blocking sends and the cycle cannot form. There is no state in which a mover is
"stuck mid-handle". Every mover always comes back around to read.

This is the same move that removed `PacedWire.mu` one branch ago: **stop sharing a
structure, give each direction its own path, and let each goroutine own its own loop.**
The outbox was a queue invented to hold messages a blocked sender could not deliver. With
no blocked senders, there is nothing to hold.

## What goes away

- `outbox` struct, `outbox.mu`, `outbox.cond`, `q []outboxItem`, `closed`
- `outbox.run` — the dedicated sender goroutine (one per mover)
- the ctx-cancel watcher goroutine inside it (one per outbox)
- the mover's inbox and the blocking `send`

**Goroutine count goes DOWN by two per mover.** Unlike the wire change (which was
goroutine-neutral), this one removes goroutines: the sender and its watcher existed only
to own the queue.

## The one real question: what happens when a channel is full

"No blocking" and "no dropping" are both required, and a bounded channel gives you a choice
of one — unless the sender keeps the item.

- **Blocking send** — forbidden. It is the deadlock, exactly.
- **Non-blocking send that drops** — forbidden. `TestOutboxFIFOPerTargetOrderNoDrop` is a
  red-proven no-drop guarantee, and it is load-bearing: a dropped `neighborSetC` leaves a
  node's layout **permanently stale**, with no later event to correct it.
- **Non-blocking send, sender retains on failure, retries next loop** — this is the answer.
  The item stays with the sender, which is going around its loop anyway. No block, no drop,
  and back-pressure becomes a local slowdown instead of a global stall.

That is the same shape the wire already uses for its delivery handoff: `outCh` full means
"retry next cycle", the bead stays in `inflight`, nothing is lost and nothing blocks. Reuse
the pattern rather than inventing a second one.

FIFO per destination must survive: a retained item is retried **before** any newer item to
the same destination.

## What must be proven

1. **The deadlock stays fixed.** `TestMutuallyAdjacentDragFloodNoDeadlock` must pass, and
   must still be a red proof — if it cannot be made to fail against a deliberately
   reintroduced blocking send, it is not testing anything.
2. **No drops, FIFO per destination.** `TestOutboxFIFOPerTargetOrderNoDrop` must pass
   against the new shape, including under a full channel forcing the retain-and-retry path.
   Force that path explicitly; do not let it go untested because the buffer happened to be
   large enough.
3. **`outbox.mu` and `cond` are gone** — deleted, not narrowed.
4. **Goroutine count drops by 2 per mover.** Assert it rather than assuming it.
5. `-race -count=3 ./...` clean.
6. Drive it in the LIVE editor. A green suite hid two animation bugs on the clock work and
   nearly produced a false bug report on the wire work; the editor is the check that counts.

## Watch for

The mover loop currently blocks waiting for inbox messages. Once the read is non-blocking,
the loop must not spin hot — it needs something to pace it, the way every other loop in the
system paces on `SleepCycle`. Decide that deliberately; a busy-wait would be a regression
that no test above would catch.
