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

## Many items, densely spaced

The chain uses **many** items, so the spacing between neighbors is **very small**.
That density is what keeps the model purely local: because every gap is tiny, an item
only ever makes a tiny midpoint adjustment against its immediate neighbors — **no node
ever computes a distance or a straight line to another node**. The straight wire is the
aggregate of many trivial local moves, never a node-to-node line calculation.

## Items are born and retired as the wire stretches or shrinks

The chain keeps its items densely and roughly evenly spaced. When a node moves, the
wire's length changes, so the **number of items changes** to hold that spacing —
locally, with no central length calculation.

- **Node dragged farther:** the gaps stretch. When an item finds its neighbor has
  drifted past an upper spacing threshold, it **spawns a new item** at the midpoint of
  that gap and splices it into the chain — the two ends relink to the newcomer. The
  wire grows item by item to fill the new length.
- **Node dragged closer:** the gaps shrink. When two neighboring items fall within a
  lower spacing threshold, one **retires**, and its two neighbors relink directly
  across the gap it leaves. The wire sheds items as it shortens.

Each item only ever measures the distance to its **immediate neighbor** — a tiny local
check for the spacing threshold — so no node computes a distance or a line to another
node. (The position relaxation itself needs no distance at all, just neighbor
positions; this neighbor-gap check is the only distance anywhere, and it is local.)
Birth and retirement are local splice / unsplice operations on the chain of goroutines;
the straightening relaxation continues unchanged around them. Holding the spacing
constant is what keeps each item's midpoint move tiny no matter how far apart the nodes
are dragged.

## A node knows its distance to a connected node — as the chain's length

Because the items hold a constant spacing, the **number of items in a chain is a measure
of the distance** between the two nodes it connects: `distance ≈ item-count × spacing`.
A node stays aware of how far it is from a connected node by **reading that count** — not
by ever measuring across space to the other node.

The count is maintained by the very same events that keep the spacing constant:

- an item is **born** (gap stretched) → the chain's count ticks **+1**,
- an item **retires** (gap shrank) → the count ticks **−1**.

So distance-awareness is a running counter, updated locally as the chain grows and
shrinks — never recomputed from coordinates and never a node-to-node subtraction. A node
becomes aware it is "**X away**" when the connecting chain reaches **N = X / spacing**
items; an awareness threshold is just a count crossing.

This only answers the distance to nodes a chain actually connects to — which is the
point: the chain that already exists *is* the ruler. (Distance to an *unconnected* node
would require reaching across empty space, which this model does not do.)

## You can't out-drag the chain — collisions need sub-pixel proximity

Because births, retirements, and the straightening relaxation all run at **machine
speed** — far faster than the user can drag or a frame can refresh — the chain fully
re-settles between input events. The practical consequence: it is **very hard to force a
collision**. As you pull nodes together the items retire and the chain relaxes faster
than your motion, so the geometry is always caught up by the time a frame draws; there is
no window in which things pile up.

The only regime where points could actually coincide is when they are **1px or less
apart** — the **spacing floor**. Below that there is nothing left to retire (items are
already at minimum spacing) and no sub-pixel room to separate them. So a collision is
only reachable at or under the pixel resolution, which is essentially impossible to hit
deliberately by dragging.

Machine speed thus turns *avoiding collisions* from an explicit rule you would have to
enforce into an **emergent property**: the geometry layer out-runs the user, and the one
remaining failure mode is sub-pixel, where it no longer matters visually.

## A retiring item hands off its bead

Density maintenance runs at machine speed while a bead traverses the chain at clock
speed, so an item can be retired while it is **carrying the bead**. A value must never be
lost to the geometry layer's churn: before an item unsplices, if it holds the bead it
**hands the bead to its next neighbor toward the destination** (downstream), then
retires. The bead continues from there.

- **Birth never threatens a bead** — inserting an item just adds one more waypoint to
  visit.
- **Only retirement can strand a bead**, so retirement is **gated on hand-off**: an item
  carrying a value relinks its two neighbors and passes the value downstream as part of
  retiring; it does not drop out while still holding the bead.

This keeps value-transport correct under the machine-speed churn of the geometry layer:
the two timescales share the chain, and the slower bead is never discarded by the faster
density changes.

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

## Two timescales: straightening is machine-speed, the bead is clock-paced

Position adjustment is **not** gated by the simulation clock and is **not** a per-frame
or per-superstep solver step. It runs at **machine speed**: when a node moves, the edge
item reacts and the correction propagates and settles as fast as the goroutines can
exchange positions and reschedule — effectively instantaneous to the viewer.

It is **event-driven**: an item recomputes and re-sends its position only when a
neighbor's position actually changes, then goes silent. Absent a perturbation the chain
is quiet — no busy-spin. The trigger is the disturbance (a node move); the chain
quiesces once it is straight again.

This keeps the two timescales cleanly separate:

- **Geometry maintenance** (the items straightening) — unpaced, machine-speed,
  event-driven on neighbor change.
- **Bead / value animation** — clock-paced; the visible motion down the chain.

## Per-goroutine ownership (the whole point)

- Each item is a goroutine that owns its own position and computes its own
  peak/valley relaxation from its two neighbors.
- The wire's shape is owned by nobody centrally — it is the collective result of
  every item self-placing. This is the per-goroutine model end to end.

## Movement keeps the chain unbroken

A node-move is felt **first by the edge item** — the item attached to that node's port
anchor. The instant the anchor moves, that edge item becomes a **peak / half-peak or
valley / half-valley** (a *half* peak/valley because only its anchor-side neighbor
jumped; its interior neighbor hasn't moved yet) and **adjusts immediately** toward its
new midpoint. That move makes its inner neighbor the next peak/valley, and the
correction **propagates inward** along the chain. Because the items are dense and each
step is a tiny move, the chain stays visually straight as the node is dragged — the
edge item's immediate reaction keeps a kink from ever appearing. No central node-move
recompute; the chain re-straightens itself.

```
   node dragged up; the EDGE item (attached to the port) reacts first:

       ●[DST]  ← anchor jumped up
        \
         ○      ← edge item: anchor-side neighbor moved, inner side
         │         hasn't yet  =  a HALF-valley
         ○──○──○──●[SRC]

   it adjusts immediately to its new midpoint; that makes the next
   item a half-valley, and the fix propagates inward, item by item,
   keeping the chain straight as the node moves:

       ●[DST]
         \
          ○─○─○─○─●[SRC]   →   eventually straight to the new anchor
```

## What this replaces

- The single two-endpoint `wireSegment` + `lerp` evaluation is replaced by a chain of
  item-goroutines that relax to straight.
- The central `NodeMoveRegistry` recompute (one lock mutating every affected edge)
  goes away: moving a node just moves an anchor; the items re-straighten locally.

## Open parameters (to settle at implementation)

- **Item count per wire** — the chain uses *many* densely-spaced items (decided); the exact number/spacing is still to be tuned.
- **Peak/valley rule confirmation** — this spec assumes "move to the midpoint of the
  two neighbors" (Laplacian); the alternative is perpendicular projection onto the
  neighbor line.
