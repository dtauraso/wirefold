---
name: project_torus_colinearity_future_equation
description: Port/edge/torus colinearity is to be rebuilt as a NEW polar equation, not the removed cartesian z-coupling
metadata:
  type: project
---

The `port ∈ torus` lock was made polar-only in `task/torus-polar-rebuild`
(merged 2026-07-05, commit 2b19c2cd): it now ONLY pins a port to its own node's
border ring via `portWorldPosAimed`'s polar ring-projection about the node
sphere. The old behavior that dragged node CENTERS in cartesian z
(`newWorld.Z = fromWorld.Z` in lockRecalc + `applyPortTorusColinearity`) was
deleted — it fought position equations and caused the `(3,r)=(6,r)` oscillation
(nodes swung to ~5165 then settled; the torus z-lock and the r-equation both
wrote a node's z).

**Consequence deliberately accepted:** an edge between two torus-locked ports is
no longer forced perfectly straight-through-both-centers (that colinearity was
the only thing the z-drag bought). Ports still snap to their rings; the edge just
bends between them.

**Future work (David's stated intent):** the colinearity between the two ports,
the edge, and the torus/ring will be rebuilt as a **new polar equation** — a
proper lock authored/solved in the polar model — NOT by reintroducing any
cartesian z-coupling of node centers. When that equation lands, it must be
structurally incapable of a position blow-up (MODEL.md: a panel-authored lock
that blows up means an offset was reconstructed from a moving reference).

Kept intact for that future equation to build on: `portTorusLocked`,
`portWorldPosAimed` / `ringProjectDir` / `partnerTorusLocked` / `ringAnchorDir`,
and the eqPortTorus authoring/persist/display path.
