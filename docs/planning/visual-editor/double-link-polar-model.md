---
branch: task/polar-double-link-model
---

# Double-link polar movement model

Goal: replace every bespoke lock (mirror, φ=0 meridian, equal-radii, bisector, the
node-3 authority flip, the meridian-carry pre-pass, and the co-sphere coupling hidden
in `RootMove`) with a COMPLETE graph of **movement links**, each carrying a **polar
equation**. It stays a lock system — a drag propagates along the links applying each
lock — but because every constrained pair has a link, every equation is polar (R/θ/φ)
and the locks chain, so the Cartesian special cases disappear. **There is no solver.**

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
either end; which node *moves to satisfy it* is a fixed DIRECTION on the lock (which end
it writes), exactly like today's locks — just now there is a link for every constraint.

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

## How it runs (no solver — it stays a lock system)

A drag fires the locks on the links touching the moved node; each writes its directed end
in polar; that end may fire its own links (the chain) — the same propagation as today. The
ONLY difference from the current system is that the link graph is **complete**: every pair
that must be constrained has a double link, so no constraint ever has to fall back to
Cartesian, and no second mechanism (co-sphere in `RootMove`, the carry pre-pass) acts
behind the locks. Each lock has a fixed direction (which end it writes); "feeders free,
mids follow" is just that direction, on the lock.

## The movement-link graph (current topology — to confirm)

Data edges are also movement links; restriction-only links (no bead) are ★.

| link        | lock (polar)                                  |
|-------------|-----------------------------------------------|
| 1↔9, 1↔10   | θ(1→9) == θ(1→10), own φ (share latitude, no swap) |
| 9↔6, 9↔2    | θ equal, φ opposite (mirror)                  |
| 2↔3, 2↔7    | θ equal, φ opposite (mirror)                  |
| 6↔5, 7↔5    | R(5→6) == R(5→7) (equal radii at 5)            |
| 10↔11, 6↔11 | R(11→10) == R(11→6) (equal radii at 11)        |
| **5↔11 ★**  | ties the two gate clusters in pure polar (coplanarity) |
| 8↔1         | feedback; no lock for now                      |

## Migration path (incremental, each step verifiable)

1. Add the link graph as declarative data (no behavior change yet).
2. Convert ONE cluster (node 5's radius lock) to fire off its links; verify against the
   current bisector behavior.
3. Convert node 11, the mirrors (9/10 → θ-only so they stop swapping; 9, 2), the
   coplanarity via 5↔11, then fold the co-sphere coupling out of `RootMove` into links.
4. Delete the old lock types + special cases once nothing registers them.

## Open questions for David

- **Link declaration site** — a `movement-links.go` registry, or topology data
  (`topology/links/`) so the links are data, not code?
- **Coplanarity links** — is 5↔11 the only ★ link, or do the feeders also need restriction
  links to each other to hold {5,6,7,10,11} coplanar by polar chaining alone?
- **9/10 swapping** — confirmed it's the `mirror` (φ flips sign so they cross); switch that
  pair to a θ-only (share latitude, own longitude) link so they stop swapping?
