---
branch: task/polar-double-link-model
---

# Double-link polar movement model

Goal: replace every bespoke lock (mirror, φ=0 meridian, equal-radii, bisector, the
node-3 authority flip, the meridian-carry pre-pass, and the co-sphere coupling hidden
in `RootMove`) with ONE uniform mechanism: an explicit graph of **movement links**, each
carrying a **polar equation**, solved by ONE propagation pass. Locks become independent
and chains compose, because there is no second mechanism doing geometry behind the
solver's back.

## Why (the friction this fixes)

- Node 11 dragged node 5 not through a lock but through `RootMove`'s **co-sphere radius
  coupling** — an undeclared movement link living in imperative code. If every coupling
  is an explicit link, "something moved that I never linked" becomes unrepresentable.
- Locks shadow each other: the BFS move-once guard lets the first lock to write a node
  win, so φ=0 silently overrides the bisector for a mid. Composition is accidental.
- Per-node registrars + special cases (node-3 flip, meridian carry) keep accreting.

## Model

Two separate graphs over the same nodes:

- **Data edges** — what you see; carry beads. (`topology/edges/`.)
- **Movement links** — NOT displayed; declare which nodes must coordinate when one moves.

A **movement link** is a DOUBLE link: bidirectional, symmetric. It holds one polar
equation `f(A, B [, ref]) = 0`. "Double" = the same constraint reads identically from
either end; which node *moves to satisfy it* is decided by the solver from which node the
user grabbed, not baked into the link as a direction.

### Link kinds (all "just polar equations" about a reference center)

| kind        | equation (polar about ref)        | replaces                |
|-------------|-----------------------------------|-------------------------|
| shareTheta  | θ_A = θ_B                          | thetaLock               |
| mirrorPhi   | θ_A = θ_B ∧ φ_A = −φ_B             | mirror locks            |
| coMeridian  | off-plane(A−B) = 0 (φ=0 plane)     | phiZeroLock             |
| equalRadius | R_A = R_B about ref                | equalRadiiLock          |
| equidistant | \|A−Mid\| = \|B−Mid\| (bisector)   | bisectorMidLock         |
| coRadius    | \|A−C\| = \|B−C\| (same sphere)    | co-sphere coupling      |

Each is a scalar polar equation; satisfying it is a projection (drop a component / rescale
a radius / project onto a plane or line). No imperative special cases.

## The three decisions (must answer before code)

1. **Authority — who follows when both ends could move?**
   A link carries a `freedom` tag per endpoint: `free` (never written by this link) or
   `follows`. Drag a `free` node → the `follows` end solves. Drag a `follows` node → it is
   projected to satisfy its own links (it is never "free"). This makes "feeders free, mids
   follow" a property ON THE LINK, not code. (A symmetric link marks both `follows`.)

2. **Conflict — two links want the same node.**
   Do NOT first-writer-win. A node with N links is moved to the point satisfying ALL of
   them simultaneously: project onto the INTERSECTION of its constraints (e.g. φ=0 plane ∩
   bisector plane = the bisector LINE — which is literally the 1-DOF line the user asked
   for). Composition is the default, not an accident.

3. **Convergence — propagation can oscillate.**
   Solve to a fixpoint: iterate the link set, projecting each `follows` node onto the
   intersection of its active links, until no node moves more than ε (cap iterations).
   Replaces the move-once BFS guard (which only ever applied ONE constraint per node).

## Migration path (incremental, each step verifiable)

1. Add the link graph + the solver alongside the existing locks (no behavior change yet).
2. Express ONE cluster (node 5: equidistant + coMeridian links) as movement links; switch
   node 5 to the solver; verify bisector + coplanar both hold (the composition the current
   code can't do). Keep the rest on the old path.
3. Migrate node 11, then the mirrors (9/10, 9, 2), then fold the co-sphere coupling out of
   `RootMove` into `coRadius` links.
4. Delete the old lock types and the special cases once nothing registers them.

## Open questions for David

- **Link declaration site** — where do movement links live? A `movement-links.go`
  registry (like the current registrars), or declared in the topology spec
  (`topology/links/`) so they're data, not code?
- **Do feeders need co-meridian at all**, or is coplanarity enough from one anchor link
  per mid? (Fewer links = simpler solver.)
- **9/10 swapping** — is that a `mirrorPhi` link doing exactly what it says (φ flips sign,
  so they cross the meridian), and you want a `shareTheta`-only link (same latitude, own
  longitude, no crossing) instead?
</content>
