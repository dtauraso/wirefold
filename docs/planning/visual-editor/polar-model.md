---
branch: task/decentralized-lock-propagation
---

# The Polar Model — node positions & movement locks

Design owner: David. Status: agreed model, pre-implementation. Governing rule:
**everything is polar; the only cartesian value in the system is the scene sphere's
own center (the world anchor). Panel-authored locks must be structurally incapable
of a position blow-up — if one happens, the implementation is wrong, not the locks.**

## Entities

### Scene sphere (the root reference)
- A **first-class entity**, NOT derived from node positions (today's
  `contentSphereFromCenters` centroid is derived — that is the wrong thing to be the
  reference; it moves when nodes move, which is circular).
- Has its **own center**, stored in **cartesian world coordinates** in the file — the
  ONE cartesian value in the whole system, the anchor to world space.
- Has a **radius long enough to fit the entire diagram**. The radius **changes when the
  user pans** (re-fit around the new center).
- **Pan moves the scene sphere** (its center), and then **recalculates every node's
  position relative to the new scene-sphere center** — i.e. each node's **scene polar is
  recomputed about the moved center**, and the radius re-fits. (This is bounded and
  user-driven, so it cannot blow up — a pan is one finite reposition, not a feedback
  cascade.) INFERRED from "the radius changes on pan": the nodes stay put in world while
  the sphere's center moves, so their distance from the new center changes (radius re-fits)
  and each node's scene polar recomputes about the new center. If instead the nodes moved
  rigidly WITH the sphere, the diagram's size relative to it — and thus the radius — would
  not change. (Confirm with David.)

### Node — two polar coordinates, nothing cartesian
1. **Scene polar** `(r, θ, φ)` about the **scene-sphere center**. This is the node's
   POSITION. It is what gets **persisted** (replacing today's cartesian `x,y,z`).
2. **Local polar** `(r, θ, φ)` — an **offset** about the **local node it is doubly-linked
   to** (the lock "center", e.g. node 2 for the `(5,φ)=(6,−φ)` lock). This is the
   **constraint frame**, and it is **stored** state, not recomputed.

A node's angle is only meaningful about a center: node 5's φ about the scene sphere and
its φ about node 2 are DIFFERENT values. Both are kept.

## Positions compose along the path (pure polar)

A node's scene position is obtained by composing the path
**scene-sphere-center → local center → node**:
- leg 1 = the local center's **scene polar** (scene center → center)
- leg 2 = the node's **local polar** offset (center → node)
- result = the node's **scene polar** (scene center → node)

This composition is a **spherical vector sum**, written in closed form on polar inputs
producing polar outputs (spherical law of cosines for the resultant radius; spherical
trig for the resultant θ/φ). **No cartesian is exposed or stored** — model purity.
(Honest note: this sum IS vector addition re-expressed in spherical terms; polar-only is
a representation choice, not a different operation. It does not by itself prevent
blow-up — see below.)

Centers may nest: compose leg-by-leg from the scene-sphere root down the chain.

## Locks are offsets

A lock constrains **one component of a node's LOCAL polar** (its offset about its
center). Example: `(5,φ)=(6,−φ)` about node 2 ⇒ node 6's local φ about node 2 =
−(node 5's local φ about node 2). The lock tells node 6 what **offset to hold** from
node 2 — nothing more.

## Movement & propagation (decentralized, bounded by construction)

- When a node moves, **both** its polars refresh — its scene polar (new position) and
  its local polar(s) — but computed from a **settled** position.
- **Rigid translate on a center move:** when a center moves, its satellites keep their
  **stored** local offset and re-derive their scene position along the path. The offset
  about *that* center is **unchanged** — it is NEVER recomputed against a center that is
  mid-move. (Pan is a DIFFERENT, top-level operation, not this rule: it moves the
  scene-sphere center and RECOMPUTES each node's scene polar about the new center +
  re-fits the radius — one bounded reposition, not a cascade. See the scene-sphere entity.)
- **Violation-check cascade:** the dragged node tells the nodes it is doubly-linked to
  that it changed. Each neighbor checks whether **its** lock (a local-polar constraint)
  is now **violated**. If violated → nudge the one constrained local component, re-derive
  its scene position, and tell **its** doubly-linked neighbors. If satisfied → **do
  nothing** (the wave dies). No queue, no collection — the channels between node
  goroutines are the propagation.

## Why a blow-up is impossible (and why today's code causes one)

The offset a node is satisfying is **stored** and merely **carried through the
composition** — it is only ever *nudged one component* by a lock or *refreshed once* from
a settled drag position. It is **never re-derived** as "angle of (node − center)" from
live world positions during a cascade. So a center that is momentarily mid-move cannot
inflate a satellite's offset; the satellite just rides its fixed offset to the center's
new spot.

Today's decentralized cascade does the opposite: `lockRecalc` recomputes the offset via
`cart2polar(node − center)` every hop, so when a center's position is mid-cascade
(e.g. node 2, which is both a lock center and a moving satellite), the recomputed offset
inflates and compounds ~3×/step to ~1e11. That recompute-from-a-moving-reference is the
bug — not the use of cartesian per se.

## Pan: world position is the invariant

On a pan the node **stays put in world** — it does not visually move. What changes is its
**scene polar**, because the center it is measured from (the scene-sphere center) moved.
So during a pan the node's **world position is held fixed** and its scene polar is
recomputed to match (and the radius re-fits). This is the mirror of a normal
node-move/lock, where the **polar is the input** and world is derived. Either way the
point's identity is preserved; which representation is the input just depends on the
operation (pan holds world fixed; move/lock changes polar).

## Persistence

The file stores:
- scene sphere **center (cartesian world)** + radius — the one cartesian anchor,
- each node's **scene polar** (replacing cartesian `x,y,z`),
- the **locks** (local-polar component constraints).

No cartesian node positions are persisted.

## Implementation phases (proposed)

1. **Scene sphere entity** — a stored cartesian center (+radius), not the derived
   centroid. Node world = compose(sceneCenter, scenePolar). Pan moves the center.
2. **Node scene polar** — persist/load node positions as scene polar; derive world for
   render.
3. **Local polar stored** — each node owns its local offset(s) about its center(s)
   (the movement-link polar already holds this; make it per-node owned, race-free).
4. **Pure-polar composition** — closed-form spherical sum for the path; no cartesian
   intermediate exposed.
5. **Cascade fix** — lockRecalc/lockNeighbors carry & nudge the STORED local polar;
   remove all `cart2polar(node − center)` reconstruction from the cascade. Rigid-translate
   on center move. Violation-check termination.
6. **Verify** — in-package test: the center-and-satellite structure that blows up today
   stays bounded and terminates; dragging a center rigid-translates its satellites.
