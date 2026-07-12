---
branch: task/tick-pause-play
---

# Split each node into two goroutines: layout (always on) + beads (pausable)

## Why

Today one goroutine per node runs a single `Update` loop that does BOTH the bead
firing/stepping AND draining the hidden layout inbox (drag/position messages).
Because one goroutine owns both jobs, anything that stops it stops both — so
"pause the animation" and "keep dragging live" are in tension. The node-1
sphere-drag bug was one instance: the loop parked waiting for tick-gated feedback
and stopped draining layout, so drags froze.

## Agreed model (A)

- **Two goroutines per node.**
  - **Layout goroutine** — brand new, spawned by the loader, one per node (NOT
    `nodeMover`, which stays as-is / demoted). It drains the node's layout inbox
    and applies position messages. It is the **sole writer of the node's
    position** (center/reach + the atomic snapshot). Always runs; play/pause does
    not touch it, so drags stay live by construction (not by a spinning loop).
  - **Bead goroutine** — the current per-kind `Update` firing loop, MINUS the
    layout-drain lines. It fires/steps beads and now READS the node's position
    from the existing atomic snapshot (it needs the center to place/aim beads and
    to compute cadence from edge length). Play/pause governs only this goroutine.

- **Position ownership (single-writer, reassigned).** Slice 3 of
  `layout-on-domain-network.md` made `Update` the sole position writer. This
  branch MOVES that role to the layout goroutine: layout goroutine = sole writer,
  bead goroutine = reader. Still single-writer by construction, just a different
  goroutine. (Deliberate reversal of that doc's "one goroutine does both"
  decision — recorded here so it is a model change, not drift.)

- **Uniform across all kinds.** The layout drain (`TryRecv`/`Handle`) leaves
  EVERY `nodes/<Kind>/node.go` `Update` loop and lives only in the shared layout
  goroutine. Per-kind loops get simpler.

- **Pause = tick-freeze only (option A, not park).** Pause still just freezes the
  one clock's tick. The bead goroutine keeps spinning on its wall-clock sleep but
  recomputes everything against the frozen tick, so nothing advances visually;
  resume is seamless (bead deadlines are in ticks, which froze). We deliberately
  did NOT add cond-based goroutine parking (option B): at this network scale the
  spin is negligible CPU and "truly idle" is only cosmetic, not worth the
  wait/wake concurrency complexity. The split alone delivers the requirement
  (animation stops, drags stay live).

## First concrete step

Introduce the dedicated per-node layout goroutine in the loader and move the
layout-inbox drain + position write into it; remove the layout-drain lines from
each `node.go` `Update`. Everything else (bead firing, edge movers) unchanged.
