---
name: project_layout_model_evolution
description: Node-layout models tried and why each was rejected, ending at the polar coordinate model — don't re-propose the dead ends
metadata:
  type: project
---

The 3D node layout went through many models (≈2 days, 2026-06-14→16) before
landing on the **polar coordinate model** (merged to main, a27ff1ec). Each pivot
rejected a model that fought the substance. Do not re-propose the dead ends; the
git history shows the *what*, not these *why*s.

**Rejected, in order:**
1. **Single vertical plane (2D-in-3D)** — too flat; couldn't express the network's
   real fan-out/depth structure.
2. **Lattice (discrete integer cells)** — removed because fan-out edges have
   *different lengths*; uniform cell spacing can't represent varying edge lengths
   (collapsed/stacked the graph). See [[project_wire_is_straight_line_not_chain]].
3. **Spheres, rooted + propagation** (anchor node 1 at origin; child = parent +
   R·dir) — rejected: the fixed anchor made node 1 un-draggable and a move only
   shifted the dragged node's subtree; a node with two parents followed only one
   sphere. "Not rooted" needs per-edge dirs / re-rooting → went away entirely
   (ALL nodes are roots).
4. **Relax / flex (PBD constraint relaxation)** — rejected: solver cascade;
   mutual/feedback pairs (8↔1) fought and froze node 8; a drag didn't resize the
   sphere the way intended.
5. **Dials / tunable-radius knobs** — rejected: knob-tuning is the wrong shape;
   it patches symptoms instead of supplying the missing local signal/contract
   (see [[feedback_go_vs_coordinator_bias]]).
6. **Three Cartesian/polar coordinate plans** — global single-pole polar (a single
   pole IS a root; multi-membership + 8↔1 cycle over-determined); per-sphere polar
   as *sole storage* (multi-parent + cycles over-determined, needs a spanning
   tree/solver); 6-coord hybrid (cartesian+polar per node, "let it have 2").

**What landed:** mostly-polar — every node has ONE outer polar coord (r,θ,φ) from
a large *container sphere* center (= prism center); **all nodes are roots** (flat,
no chain). Cartesian only at boundaries: camera pan/zoom, render, saved files.

**The two reframes that took longest to find (the actual unlocks):**
- **Over-determination dissolves with derive-on-read:** roots are the only stored
  state; sphere R, surface coords, tori normals are *measurements* computed from
  roots when drawn, never stored — so no solver, no stale-coordinate freeze.
- **"On the surface" means EQUIDISTANT:** surface nodes share the sphere radius;
  dragging one resizes the sphere and the others scale radially. (An earlier
  "soft membership" = R is max-distance/siblings independent was wrong: dragging an
  inner node couldn't shrink a sphere pinned by its farthest node.)

Relates to [[feedback_derive_model_from_visual_spec]] and
[[feedback_specify_go_layer_first]].
