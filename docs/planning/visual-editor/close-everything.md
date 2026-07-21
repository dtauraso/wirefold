---
branch: task/mutex-trace
---

# Breadcrumb is not special, and shutdown should be complete

## The two things holding `Trace.mu` in place

Neither is the accumulator. In production nothing races `events`: the drain goroutine is
its sole writer, `Events()` has no non-test callers, and `WriteJSONL` runs after `Close()`
when drain has already exited.

What actually needs the lock:

1. **`Breadcrumb` is special.** Every other event goes producer → `t.ch` → drain → sink.
   `Breadcrumb` writes `sink`/`debugSink` **directly**, from whatever goroutine called it.
   So two breadcrumbs can interleave mid-line on stdout, and one can collide with `Close`.
2. **Shutdown is ragged.** `main.go` waits for node goroutines only, and only for 250 ms.
   Movers, wire goroutines, and the stdin reader are never waited on at all. So `Close()`
   runs while goroutines are still alive and still able to call `Breadcrumb`.

Fix both and the lock has nothing left to guard.

## Fix 1 — breadcrumbs go through the channel

`Breadcrumb` becomes an ordinary event: send on `t.ch`, let the drain goroutine write it.

    TODAY   any goroutine ──direct──▶ debugSink (os.Stdout)   ← needs a lock
    THIS    any goroutine ──t.ch──▶ drain ──▶ debugSink       ← one writer, no lock

This is the same ownership move as everywhere else: one goroutine owns the sink, everyone
else sends. It also fixes an unrelated latent bug — today a slow stdout blocks every other
breadcrumb caller behind it, because the write happens under the lock on the caller's own
goroutine.

Note what must be preserved: breadcrumbs are a DEBUG channel and must not be reordered
into meaninglessness relative to the events around them. Going through the same channel
as everything else actually improves that — today a breadcrumb and the events near it are
ordered by two different mechanisms.

## Fix 2 — wait for everything, then close

`wg.Add(len(nodes))` covers node `Update` loops. It does not cover:

- `nodeMover.run` — one per node (`node_move.go:541`)
- `edgeMover.run` — one per edge, and these are the WIRE goroutines (`node_move.go:544`)
- `RunStdinReader` (`main.go:196`)

All of them already exit on ctx. They are simply not waited on. Add them to the WaitGroup
so `wg.Wait()` means what it says.

### The 250 ms grace is padding

Measured claim, not assumed: every `wg`-tracked goroutine's only blocking call is
`SleepCycle`, which selects on `ctx.Done()`. Worst case from cancel to return is ~16 ms —
one tick. No code path was found where a tracked goroutine can exceed 250 ms.

So the grace can go once the wait is complete. If something ever does hang, a visible hang
is better than a silent race — and unlike today, the hang would name the goroutine that
failed to exit.

### The one genuinely unbounded path

`RunStdinReader`'s frame reader blocks in `io.ReadFull` on `os.Stdin` and does NOT select
on ctx. It is unblocked by closing the fd, which works because production stdin is a pipe
(pollable). On a non-pollable fd it would block indefinitely.

That is a real gap, it is NOT what the 250 ms was protecting (that goroutine was never in
the WaitGroup), and it should be handled deliberately when adding it to the group — not
papered over with another timeout.

## Then the lock goes

With one writer to the sink and a shutdown that actually waits:

- `sink`/`debugSink` — written only by drain. No lock.
- `events` — already drain-only. No lock.
- `closed` — an idempotency flag. `sync.Once` expresses that directly.

`Trace.mu` is deleted, and the repo has zero mutexes in non-test Go.

## What must be proven

1. `Trace.mu` deleted, not narrowed.
2. Breadcrumbs still reach `.probe/go-debug.jsonl` with the same shape — this is a live
   debugging channel (CLAUDE.md), and silently losing it would be worse than the lock.
3. Shutdown genuinely waits: assert no goroutine outlives `Close()`.
4. `-race -count=3 ./...` clean.
5. The existing `Trace/trace_concurrency_test.go` red-proofs must be re-examined, not just
   deleted. They exist to prove the lock is load-bearing; if the lock goes, they should be
   rewritten to prove the new invariant (single writer) or deleted with reasoning — not
   left asserting something that no longer exists.
6. Drive the editor: breadcrumbs are how Go-side debugging works, so a regression here is
   felt later, in the worst way.
