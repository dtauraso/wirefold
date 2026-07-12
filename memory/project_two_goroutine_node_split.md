---
name: project_two_goroutine_node_split
description: Each node runs two goroutines — always-on layout goroutine + pausable bead loop; pause governs only beads
metadata:
  type: project
---

Merged to main 2026-07-12 (task/tick-pause-play). Each node is TWO goroutines,
reversing the earlier "one Update goroutine does both" decision in
`layout-on-domain-network.md`:

- **Layout goroutine** — `LayoutPort.run` (nodes/Wiring/layout_edge.go), one per
  node, launched by `MoveDispatch.Start`. Blocks on the layout inbox and applies
  drag/position messages via `Handle`. It is the SOLE writer of the node's
  position (`applyCenter` runs on it). Always on — play/pause never touches it,
  so dragging stays live by construction (paused or running).
- **Bead goroutine** — the per-kind `Update` firing loop, with the layout-drain
  block removed from all kinds. Pure position READER (atomic snapshots). Pause =
  tick-freeze governs ONLY this goroutine (option A: spin-through-pause, NOT
  cond-parked; parking was rejected as negligible CPU + cosmetic at this scale).

Why: one goroutine owning both jobs coupled "pause animation" and "keep dragging
live" (the node-1 sphere-not-moving-on-drag bug). The split makes that bug class
unrepresentable — no bead loop drains the layout port, so nothing it waits on
(missing feedback, a frozen tick) can stall a drag. See
[[feedback_go_vs_coordinator_bias]].

Known deferred bug (branch task/drag-cascade-upstream-leak): the iR drag cascade
seeds from the dragged node's reference and forwards along hidden layout edges
that mirror domain edges one-for-one WITHOUT direction filtering, so it leaks
UPSTREAM through feedback back-edges — dragging node 5 also moves its root node 1.
Should reach descendants/siblings only, not ancestors.
