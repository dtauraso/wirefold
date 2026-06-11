---
name: Go visual vocabulary
description: The Go rebuild only needs two visual primitives — chan→wire and a per-node running indicator (with reloop). Goroutine and select are not separate visual concepts.
type: project
---

The Go rebuild's visible-state vocabulary collapses to two primitives:

1. **chan → wire** — a value moving along a wire between nodes.
2. **per-node running indicator** — shows that a node is executing, with an optional reloop affordance for nodes that loop back on themselves.

Goroutine and select are *not* separate visual primitives:
- A goroutine is just "this node is running" (the activity indicator).
- A select is just "this node is waiting on multiple incoming wires, one fires" — emergent from wire-firing + node-running, not a distinct shape.

**Why:** User narrowed scope on 2026-05-07 after dropping the goro-sched and select sketch pairs from `docs/planning/sim-go/`. The collapse aligns with the go-vs-coordinator bias: goroutine and select shapes risk pulling visuals toward orchestrator-style diagrams instead of letting behavior emerge from local channel + node activity.

**How to apply:** When writing the rebuild plan (step 3 / Gate A) or designing later Go visuals, do not propose separate visual contracts for goroutine lifecycle or select fairness. Pin contracts on channel FIFO, select determinism, and scheduler determinism at the *semantic* level, but render them with only the two primitives above. If a future sketch needs goroutine or select as a distinct visual, treat that as a scope expansion requiring explicit user sign-off.
