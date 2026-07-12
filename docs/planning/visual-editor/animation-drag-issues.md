---
branch: task/layout-rebuild-on-domain-network
---

# Open issues — animation & dragging (live-observed)

Status as of the current live test on `task/layout-rebuild-on-domain-network`
(one-clock sleep-only model landed; Input reworked to a periodic source; node 4
removed; node 1 → {2,3}). A diagnostic `applyCenter` breadcrumb is currently in
`nodes/Wiring/node_move.go`.

## 1. Pause does not freeze node 1's internal bead animation
- **Observed:** with the sim paused, node 1's interior beads keep animating /
  flickering instead of freezing.
- **Cause (identified):** node 1 is now a self-firing SOURCE. Its loop paces on
  `clk.SleepCycle` which is real-time (ignores Halt by design, so drags stay
  live). Because the loop keeps cycling while paused and the fire cadence was
  driven by real-time cycles, node 1 keeps PLACING new beads during pause. They
  pile up at the frozen start position → flicker. (Other node kinds don't self-
  fire — they fire on received input, which is frozen during pause — so they
  don't show this.)
- **Requirement (David):** node 1 must run the SAME sleep-timer loop as every
  other node kind, differing ONLY by the extra multiplication factor (edge
  length) on the sleep. Pause behavior must match the other nodes (freeze),
  achieved the same way they achieve it — not by a per-node special case.

## 2. Beads flickering (node 1)
- Same root cause as #1 (over-placing beads because emission isn't tied to the
  pause-aware clock the way the other nodes' work is).

## 3. Drag node 5 → cascade descendants' edges flicker / lag
- **Observed:** dragging node 5 moves 5, 7, 8. The drag origin (5) moves cleanly
  with its edges. Cascade descendants' edges now follow (after the
  `applyLayoutCenter` edge-notify fix) but FLICKER — the edge can't stay where
  the node is moved to.
- **Cause (hypothesis, unconfirmed):** the drag origin's edges update
  immediately on the drag/dispatch goroutine (`fanCenters`), but a cascade
  descendant's edges now update on that descendant's OWN loop at the ~human-cycle
  sleep cadence — one hop behind the drag. During a fast drag the descendant
  edges chase and snap → flicker. This is a lag inherent to routing cascade edge
  updates through the per-cycle node goroutines.

## 4. (Earlier, still unexplained) node 1 sphere did not move on drag
- Original symptom before the Input rework: dragging node 1 moved only its edges,
  not its sphere. The "loop exited so it can't be dragged" theory was DISPROVEN —
  node 1 is `repeat:true` and loops forever, draining its layout port each cycle.
- Root cause NOT yet confirmed. Next diagnostic step: drag node 1 and read
  `.probe/go-debug.jsonl` for `applyCenter node="1"` — present ⇒ Go updates the
  center, bug is TS-side (sphere mesh not repositioned); absent ⇒ node 1's loop
  isn't draining its layout port on drag (Go-side).

## Guiding constraint (David)
Stop proposing one-off/per-node solutions. Node behavior should be uniform: the
same sleep-timer loop across kinds, pause-awareness coming from the one clock
(Tick freezes on Halt), drags staying live because the loop keeps cycling and
draining the layout port. Input differs from the others by ONE thing only: the
multiplication factor on its sleep.
