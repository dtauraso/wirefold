---
branch: task/mover-nonblocking-cascade
---

# Cascade deadlock fix — spec

## The bug (confirmed)

Two mutually-adjacent nodes can deadlock. A `nodeMover.run` loop (node_move.go:615)
drains one message and calls `handle` **synchronously**; `handle` (via
`fanEdgesAndPartners`/`handleTrigger`) sends to neighbor movers with a **blocking**
`ch <- msg` into an 8-deep inbox. While a mover is inside `handle`, it is NOT draining
its own inbox. So if node A and node B are neighbors, both mid-`handle`, both inboxes
full, A blocks sending to B and B blocks sending to A — neither drains, both hang.

The requantize kinds already dodge this via `sendMoveLossy` (drop-if-full,
node_move.go:1170), safe because a dropped requantize self-heals on the receiver's
next commit. The **position-carrying** sends (equalize, trigger, gatePlace, centers,
nil-Center partner re-emit) stay blocking on purpose: dropping one leaves geometry
stale with no self-heal. Termination is by **idempotence**, not acyclicity — the graph
HAS cycles (handleTrigger L570 comment), so inbox depth is the only thing preventing
the deadlock today.

## Constraint that rules out "buffer + retry" (important)

A buffered-and-retried send that captures an absolute position and delivers it late —
or out of order relative to a newer update — would VIOLATE the idempotence the cascade
relies on (node_move.go:1357-1368 warns a stale absolute Center can clobber a pending
real write). So the fix MUST:
- drop nothing (position sends have no self-heal), and
- preserve per-target FIFO order (newest update must not be overtaken by an older one).

"Latest wins" holds only because messages to a given target arrive in send order; any
fix must keep that.

## Chosen design: per-mover outbound sender stage

Decouple *sending* from *handling* so the handler goroutine never blocks on a send and
therefore always keeps draining its own inbox — which structurally removes the circular
wait. Concretely, per mover (node and edge):

- Add an **unbounded FIFO outbound queue** owned by the mover (slice + `sync.Mutex` +
  `sync.Cond`, or equivalent), and a dedicated **sender goroutine** that pops messages
  in order and does the existing **blocking** `sendMove` to the target inbox.
- `handle` (and everything it calls: `fanEdgesAndPartners`, `handleTrigger`,
  `moveNodeAndSetEdgeCs`) **enqueues** to this outbound queue instead of doing
  `ch <- msg` / `md.sendMove`. Enqueue never blocks (unbounded queue).
- The handler goroutine's `run` loop is unchanged except that its sends are now
  enqueues; it returns from `handle` promptly and loops back to draining `inbox`.

### Why this is correct

- **No deadlock:** the handler goroutine never blocks on a send, so it always drains
  its inbox. The sender goroutine may block on a full target inbox, but that target's
  handler is always draining, so room always appears. No cycle of blocked handlers.
- **Nothing dropped:** unbounded queue + blocking send = every message delivered.
- **Order preserved:** a single sender per mover pops FIFO, so per-target send order is
  exactly the enqueue order — same as today's synchronous sends. "Latest wins" holds.
- **Idempotence untouched:** message content is captured at enqueue time and delivered
  in order, identical to the current blocking send — we are not re-deriving from a
  stale snapshot (the L1357 hazard), just deferring the send by a goroutine hop.
- **Model-consistent:** still fully decentralized — each mover gets private local state
  (its outbound queue + sender goroutine). No central queue, no global lock, no
  coordinator. `md.dispatch` stays a read-only directory.

### Bounding / termination

Cascades are finite (idempotent quiescence), so the outbound queue drains to empty and
does not grow without bound in practice. The queue is unbounded by design (a bound would
reintroduce a blocking-enqueue deadlock). If we want a safety valve, add a breadcrumb
when a queue exceeds a high-water mark (diagnostic only, never drop a position message).

### Lossy requantize path

`sendMoveLossy` can stay as-is (drop-if-full is still valid for requantize) OR route
through the same outbound queue; keeping it lossy is simpler and preserves current
behavior. Spec: leave `sendMoveLossy` unchanged; only the blocking position sends move
to the outbound queue.

## Call sites to convert (from the structural map)

All of these currently do a blocking send mid-`handle` and must become enqueues:
- `fanEdgesAndPartners` raw `ch <- msg` → edges (node_move.go:1327, Centers)
- `fanEdgesAndPartners` raw `ch <- msg` → partners (node_move.go:1368, nil-Center)
- `handleTrigger` → gates (L516, gatePlace), followerOwner (L519, trigger),
  sourceID (L544, equalize), each follower (L567, equalize), each forwardTarget
  (L579, trigger)

Sends NOT on a handler goroutine (safe to leave blocking, but converting is harmless and
uniform — decide during build): `applyRingAnchor` (gesture.go:644,653, gesture goroutine),
`rootMoveViaMessages` (L1832, gesture goroutine). Leave these blocking unless uniformity
is cleaner; they cannot be part of a handler-to-handler cycle.

Lossy sends stay: `setEdgeC`→requantizeSetC (L1679), `requantizeLocalPolars` (L1909).

## Lifecycle

The sender goroutine is spawned alongside each mover in `newMoveDispatch`/`Start`, under
the same process-lifetime ctx, and exits on ctx cancel (drain-and-exit, or exit
immediately — cancel means shutdown, no need to flush). Mirror the existing mover
goroutine lifecycle; no per-gesture spawn.

## Verification

- New test `TestMutuallyAdjacentCascadeNoDeadlock`: construct two mutually-adjacent
  nodes, fill both inboxes, drive concurrent commits that each fan to the other, assert
  both movers make progress / the drive completes within a timeout. Must HANG on the
  old blocking code (verify by temporarily reverting) and PASS with the fix.
- Full `nodes/Wiring` package incl. the `-race` tests (`TestMoverCenterRace`,
  `TestOutGeomRace`, `TestNode6DragNoDataRace`) and the cascade/tap tests in
  node5_equalize_test.go / node_move_test.go — behavior and message traffic must be
  unchanged (order preserved).
- Full `scripts/stop-checks.sh`.

## Out of scope

No change to what messages are sent, when a node fires, or the equalize/trigger math —
only the transport (blocking-send-in-handle → enqueue + sender goroutine).
