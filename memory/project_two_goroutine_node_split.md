---
name: project_two_goroutine_node_split
description: The two-goroutine-per-node split (LayoutPort.run + bead Update) described here was superseded by decentralized nodeMover goroutines (node_move.go); LayoutHolder.UpdateLayout is a vestigial no-op, no longer spawned by main.go (2026-07 code-smell audit)
metadata:
  type: project
---

STALE as of a 2026-07 code-smell audit — kept for history, do not act on the mechanism
below as current.

What this memory originally described (merged 2026-07-12, task/tick-pause-play): each
node ran TWO goroutines — a `LayoutPort.run` (in a now-deleted layout_edge.go) always-on
layout goroutine handling drag/position, plus the pausable bead `Update` firing loop.

That `LayoutPort`/layout_edge.go mechanism no longer exists in the codebase. Node-move
today is the DECENTRALIZED `nodeMover` goroutine model (`node_move.go`, one inbox per
node, `MoveDispatch.Start` launches them) described in
`project_lock_propagation_decentralized.md` — read that one instead.

Separately, `Wiring.Node` still declares `UpdateLayout(ctx)` (satisfied via the embedded
`Wiring.LayoutHolder`, `layout_holder.go`), documented as "a pause-independent
layout-update goroutine." Its body is `<-ctx.Done()` — no runtime mutation — and it was
never wired to the real nodeMover drag path. main.go used to spawn one goroutine per
node just to block on it; that spawn loop was deleted (dead weight: N goroutines + N
WaitGroup slots existing only to park). The `UpdateLayout` method and interface
requirement were left in place (every kind still compiles against `Node`), but nothing
calls it. If a future slice needs a genuinely pause-independent per-node loop, that is
the seam to revive — wire it to something the nodeMover model actually needs, don't
resurrect the old LayoutPort shape.
