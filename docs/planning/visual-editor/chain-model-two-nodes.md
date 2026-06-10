---
branch: task/go-backend-ts-frontend
---

# Spec — two nodes connecting (the chain model)

**Status: proposed.** Pending sign-off before folding into MODEL.md. Authored to
resolve the node-move ownership fork (who re-emits an edge whose endpoints live on
two different goroutines).

## Premise

There is no standalone "edge" entity sitting between two nodes. **A wire is part of
a node.** The topology is an unbroken **chain of links**; each link is a node body
plus the wire(s) leaving it. Port boundaries are internal seams the viewer never
sees — the chain reads as one continuous run, no gaps.

## The minimal case: A → B

- **Link A** = node A's body + its outgoing wire `w`.
- **Link B** = node B's body (plus its own outgoing wires; none in this case).
- Wire `w`'s **near end** is anchored at A's output port; its **far end** is anchored
  at B's input port. That far-end/in-port coincidence is the **seam** — one shared
  point, not a gap.

## Wire shape

The wire is a **straight line** between the two ports. There is no curve math and no
length minimization — the segment is simply the straight connection from the source
output port to the destination input port. When either endpoint moves, the segment is
redrawn straight between the new endpoints.

## Ownership (the whole point of the chain)

- **Wire geometry** — a **straight line segment** from A's output port to B's input
  port (no curve, no control points), plus its length — belongs to the **source link**
  (A). A emits one geometry event per outgoing wire on startup. (Already true: the
  per-goroutine edge-curve emit makes the source node emit its outgoing wires.)
- **Bead delivery slot + timing** at the far end belongs to the **destination** (B's
  input port owns the `PacedWire` slot; fan-in-safe). A bead is emitted by A, travels
  A's wire, and lands in B's slot.

So a connection has exactly one owner for the wire's shape (the source) and one owner
for the bead's landing (the destination) — never the ambiguous shared-edge case.
Geometry is the source's; the landing is the destination's.

## Connecting = joining two links at a seam

B's input port sits exactly where A's wire ends. The two links abut at that point.
Because the wire is part of A (not a free-floating edge), the chain `…→[A‖w]→[B]→…`
always has a single, unambiguous owner per segment.

## Movement keeps the chain unbroken

A node-move is delivered to the moved node and to its **immediate chain neighbors** —
no central edge registry arbitrating shared edges.

- **Move A:** A recomputes wire `w` (its near end moved) and re-emits `w`. Fully local
  to link A.
- **Move B:** B does not own `w`'s shape, but the seam moved. B notifies its upstream
  link A ("my input port moved"); A re-anchors `w`'s far end and re-emits. B updates
  its own incoming delivery slot/timing.

Each link only ever re-emits wires it owns; the seam is re-joined by the owning
(source) link, so the chain never shows a gap mid-move.

## What this replaces

The central `NodeMoveRegistry` recompute (one lock mutating every affected edge's
`Out`) goes away. Move handling becomes per-link: deliver to neighbors, each re-emits
the wires it owns.

## Open implementation question (not part of the model)

Nodes spend most of their time parked in a blocking `pw.Recv()` and have no per-node
inbox for editor input. Delivering a move to a parked link's goroutine — and waking it
so a drag during a paused sim still redraws — is an implementation detail to settle
after this model is signed off.
