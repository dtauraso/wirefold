---
branch: task/go-backend-ts-frontend
---

# Spec — two nodes connecting (the item-chain model)

**Status: proposed.** Pending sign-off before folding into MODEL.md. Supersedes the
earlier single-segment framing: a wire is now a chain of relaxing items, and its
straightness is *emergent* from per-item local relaxation, not a `Start+t·(End−Start)`
formula.

## Premise

A wire between two nodes is not a single segment nor a curve. It is a **chain of
items**:

```
source node → item₁ → item₂ → … → itemₙ → destination node
```

```
   source                                  destination
   node                                           node
    ●━━━━━○━━━━━○━━━━━○━━━━━○━━━━━●
   out    i₁    i₂    i₃    i₄    in
   port                          port

   ●  fixed anchor (a node's port)
   ○  item — its own goroutine, free to move
   ━  adjacency between neighbors (not a drawn curve)
```

Each item is its own **goroutine**. There is no central solver that positions the
wire; each item self-places from its neighbors.

## An item

- Has exactly **two ends** — one neighbor on each side.
- Interior items neighbor two items. The **first** item's outer neighbor is the
  **source node's output port**; the **last** item's outer neighbor is the
  **destination node's input port**. Those two ports are the chain's fixed **anchors**.
- Owns its own **position**.
- Is a **goroutine**.

## Straightening: each item removes its own peak/valley

Each item, on its own goroutine, repeatedly:

1. Reads its own position and its two neighbors' positions.
2. Considers the lines drawn to each neighbor. The bend at the item makes it a
   **peak** (bulging one way) or a **valley** (bulging the other); if the item lies on
   the straight line between its two neighbors it is **neither**.
3. If it is a peak or valley, it **moves onto that line** — to the **midpoint of its
   two neighbors** — so it is neither.

```
   PEAK                          VALLEY
   (item above the                (item below the
    neighbor line)                 neighbor line)

        ○ i                       A ●─────────● B
       / \                             \     /
      /   \                             \   /
   A ●     ● B                           ○ i

   neither (already straight):   A ●────○────● B
                                        i
```

```
   one relax step at item i (neighbors A, B):

   before:  A ●        ● B      after:  A ●────●────● B
                \      /                       i
                 \    /                 i ← midpoint(A, B):
                  ○ i                   now on the A–B line,
              (a valley)                neither peak nor valley
```

```
   t₀  jagged (right after a node moves):
            ○             ○
           / \           / \
       ●--/   \--○--   --/   \--●
                  \   /
                   \ /
                    ○        (peaks & valleys)

   t₁  one relax step (each item → midpoint of neighbors):
             ○         ○
            / \       / \
       ●---○   \--○--/   ---●
                 bumps shrink

   t∞  converged — straight, evenly spaced:
       ●----○----○----○----○----●
```

All items do this concurrently and locally. With the two anchors fixed (source
out-port, dest in-port), the chain **relaxes to a straight line** between them.
Straightness is **emergent** from local per-item relaxation — not computed from a
curve or segment formula.

## The bead

A bead carries a **value** and **visits each item in sequence** as its animation:

```
source → item₁ → item₂ → … → itemₙ → destination
```

```
   the bead ◉ carries a value and lands on each item in turn:

   step 0   ●◉───○───○───○───○───●    value enters at source
   step 1   ●───◉───○───○───○───●
   step 2   ●───○───◉───○───○───●
   step 3   ●───○───○───◉───○───●
    ⋮
   step n   ●───○───○───○───○───◉●   value delivered at destination
```

The bead's motion is the hop from item to item along the chain.

## Per-goroutine ownership (the whole point)

- Each item is a goroutine that owns its own position and computes its own
  peak/valley relaxation from its two neighbors.
- The wire's shape is owned by nobody centrally — it is the collective result of
  every item self-placing. This is the per-goroutine model end to end.

## Movement keeps the chain unbroken

When a node moves, only its terminal **anchor** (the port the chain attaches to)
changes. The adjacent item sees its neighbor moved on its next check and relaxes
toward the new midpoint; the relaxation propagates down the chain until it is straight
again. No central node-move recompute — the chain re-straightens itself.

```
   before:
       [SRC]●───○───○───○───●[DST]

   node dragged — the in-port anchor jumps; the last item is now a valley:
                             ●[DST]
                            /
       [SRC]●───○───○───○──○
                         (valley)

   the chain relaxes (each item → midpoint) until straight to the new anchor:
       [SRC]●──○──○──○──○──●[DST]
```

## What this replaces

- The single two-endpoint `wireSegment` + `lerp` evaluation is replaced by a chain of
  item-goroutines that relax to straight.
- The central `NodeMoveRegistry` recompute (one lock mutating every affected edge)
  goes away: moving a node just moves an anchor; the items re-straighten locally.

## Open parameters (to settle at implementation)

- **Item count per wire** — how many interior items a wire is divided into.
- **Relaxation cadence** — items relax continuously on their goroutines; the
  check/step rate and how it's paced against the clock.
- **Peak/valley rule confirmation** — this spec assumes "move to the midpoint of the
  two neighbors" (Laplacian); the alternative is perpendicular projection onto the
  neighbor line.
