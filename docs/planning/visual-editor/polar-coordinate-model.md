---
branch: task/spherical-layout
---

# Polar coordinate model

Status: design (supersedes the Cartesian `meta.json` node layout).

This is the spatial model for the diagram: a container sphere, polar-only
nodes anchored to it, and per-node nested spheres whose surface nodes are
described in polar coordinates and kept consistent by links. Cartesian is
confined to the camera and the render boundary — **no node stores or uses
Cartesian.**

## 1. The container

- The diagram lives inside one **large sphere**.
- The large sphere's **center is the common origin** — the single pole all
  node positions are measured from. It is a *container*, not a node, so no
  node is privileged (the model stays non-rooted in the node sense).
- The **camera** sits on the large sphere's surface and can move inside it.
- A **rectangular prism encloses the large sphere** ("houses the diagram").
  Its center coincides with the sphere center = the polar origin, and its
  axis-aligned edges define the Cartesian frame used for **saved files** (§8a).
  The prism is the fixed reference that makes Cartesian ↔ polar conversion
  deterministic at the storage boundary.

## 2. Coordinate dimensions

Polar (spherical) coordinates, using the scene axes **+x right, +y up
(vertical), +z toward camera**, with the **vertical axis +y as the pole**:

- **`r` — radius.** Distance from the origin. Selects which shell/sphere the
  point is on. All nodes on one sphere share `r`.
- **`θ` (theta) — polar angle / latitude.** Angle from the up axis (+y).
  `θ=0°` straight up, `θ=90°` equator, `θ=180°` straight down. Changing `θ`
  alone slides along a meridian (pole-to-pole).
- **`φ` (phi) — azimuth / longitude.** Angle around the vertical axis in the
  x–z plane. `φ=0°` toward +x, increasing toward +z. Changing `φ` alone
  spins around the vertical axis along a circle of latitude.

Cartesian conversion (render/input boundary only):

```
x = r·sinθ·cosφ
y = r·cosθ
z = r·sinθ·sinφ
```

## 3. Nodes are polar-only

- **All nodes are roots.** Every node's authoritative position is **one outer
  polar coordinate `(r, θ, φ)` measured straight from the large sphere's
  center.** No node is parented to another for its position; they are all
  equal, all anchored to the container. (Flat, not a deep tree — this is what
  removes the 8↔1 feedback cycle as a position-dependency problem.)
- **Every node is also a sphere center.** As a center it holds the **polar
  coordinates of the nodes on its own surface**, measured *from itself*. These
  are secondary, linked views (see §4).
- **No Cartesian on a node.** Ever.

```go
type NodePolar struct {
    // Outer: this node's position from the large sphere's center (authoritative).
    R, Theta, Phi float64
    // As a center: polar coords of the nodes on this node's own sphere,
    // measured from this node. Keyed by surface-node id.
    Surface map[string]Polar
}

type Polar struct{ R, Theta, Phi float64 }
```

## 4. Links keep coordinates consistent

A node that sits on another node's sphere appears in **two places**:

1. its own **outer** coordinate (from the container) — authoritative, and
2. a **surface** coordinate stored by the center whose sphere it is on — a
   linked view of the same world point.

There is exactly **one authoritative coordinate** per node (the outer one).
The surface coordinate is derived. A **link** binds them so that when the node
moves, both update **at the same time** — no solver, no over-determination,
because nothing is independently asserted.

Multi-membership (e.g. node 5 on node 6's sphere) and the 8↔1 feedback ring
are handled purely by links: one stored outer coordinate per node, links
re-derive every surface coordinate that references it.

## 5. Which system does what

- **Polar** — node positions, and **camera rotation** (orbit).
- **Cartesian** — only:
  - camera **pan** (2D translation) and **zoom** (translation), and
  - the **render boundary**: three.js needs Cartesian to plot a point and to
    raycast the mouse, so polar → Cartesian conversion happens there as a
    transient render output. Never stored, never authoritative.

## 6. Move flow (polar end to end)

- **Move a node on the outside (mouse drag):** cursor hit arrives as Cartesian
  at the input boundary → converted immediately to a polar update → the node's
  **outer `(r, θ, φ)`** changes → links update every center's surface
  coordinate for that node, and (if the node is a center) its surface children
  re-derive → renderer converts the new polar back to Cartesian only to draw.
- **Move a node on a surface:** change `θ, φ` (and `r` for in/out) in the
  relevant frame → outer coordinate + links re-sync.
- **Resize a sphere:** change the center's surface `r` for its surface nodes →
  links re-sync.
- **Change the origin:** moving a center translates its sphere; its surface
  nodes keep their surface polar values and re-derive.

Cartesian appears for one instant at input (mouse) and one instant at output
(draw); nothing in the model stores it.

## 7. Worked example: the perpendicular-chord lock

This is the canonical example of a **link** (§4) and of why polar fits.

In Cartesian this lock was painful: nodes 2 and 6 are the two ends of a chord
perpendicular to a vertical disk of node 1's sphere, midpoint on the disk,
endpoints on the surface — i.e. `(px, py, Cz ± √(R²−d²))`, with a special-case
post-pass to keep them consistent.

In node 1's local polar frame (pole = +y) the same lock is **one invariant:**

> **node 6 = mirror_φ(node 2):** same `r`, same `θ`, azimuth `φ` vs `−φ`.

Geometrically (see §2):

- **same `r`** → both on node 1's sphere surface,
- **same `θ`** → both on the same **horizontal latitude ring** (same height,
  same distance from the vertical axis),
- **`φ` vs `−φ`** → mirror-imaged across the **`φ = 0` vertical disk** (the x–y
  plane), so they share x,y and have ±z — a horizontal chord along `z`,
  perpendicular to that disk, midpoint on it.

Verify against `x = r·sinθ·cosφ, y = r·cosθ, z = r·sinθ·sinφ`: flipping the sign
of `φ` flips only `z`; `x` and `y` are unchanged. Midpoint `z` averages to 0, so
it lands on the disk. No square roots, no post-pass.

Drag rule: drag node 2 → set its `(θ, |φ|)` from the cursor; node 6 is
`(R, θ, −φ)`. The pair stays a perpendicular chord automatically. A "lock" is
therefore not a special case — it is a coordinate relationship plus a link.

## 8a. Storage: the prism frame (resolves Q1)

- **Runtime is polar.** Nodes hold polar coords; no Cartesian at runtime.
- **Saved files are Cartesian in the prism frame.** On **save**, Go converts
  each node's polar coord → Cartesian relative to the enclosing prism (§1). On
  **load**, Go converts prism-Cartesian → polar. The prism center = the sphere
  center = the polar origin, so the conversion is well-defined and stable
  across save/load.
- This makes the **storage boundary** a third place Cartesian appears
  (alongside camera + render, §5) — and the only place it touches disk.
- **Prism is built from the current Cartesian points** (resolves the shape
  question): it is the **axis-aligned bounding box** of the existing node
  positions — a general rectangular prism, not a forced cube. Its **center**
  (midpoint of the box) becomes the polar origin / large sphere center. The
  large sphere's radius circumscribes the nodes (max node distance from the
  center) so every node sits inside it.

## 8b. Bootstrap / migration: roots first (resolves Q2)

Making the roots *is* computing the large-sphere polar coords — a root is
nothing more than its outer `(r, θ, φ)`. Everything else derives from roots +
edges, so the one-time migration has a clean order:

1. **Build the prism** from the current Cartesian node positions (§8a): the
   axis-aligned bounding box; its center is the polar origin.
2. **Make the roots.** For each node, `outer = cart2polar(pos − prismCenter)`.
   Every node now stands alone, anchored to the container — no parent. This is
   the migration of the existing 8-node topology.
3. **Derive surfaces + links.** For each sphere edge (center → surface node),
   the center computes that surface node's polar coord relative to itself (from
   the two known roots), and a link binds it back to the surface node's root.

No chicken-and-egg: roots are measured from the container, never from each
other, so a root never depends on another node's surface coord. Roots are
independent; surfaces are downstream.

## 8c. Derived quantities & soft membership (resolves Q3)

**Link implementation = derive on read (Option A).** Roots are the single
stored authority. Both **surface coordinates** and a center's **sphere radius
R** are *derived on read*, computed from roots when something needs them
(rendering). Nothing is stored redundantly, so nothing can go stale and no
push/sync logic exists. Every edit — outside drag or surface move — ultimately
writes the moved node's **root**; surface coords and R are recomputed lazily.

**Co-sphere radius coupling (surface nodes are equidistant).** "On the surface"
means every surface node of a center is at the same distance R from it — they all
lie ON the sphere. Dragging a surface node *resizes the sphere*: the new R is the
dragged node's distance to the center, and every OTHER surface node of that center
moves RADIALLY to the new R, keeping its own direction from the center. (Earlier
this was "soft membership" — R = max distance, siblings independent — but then
dragging an inner node couldn't shrink a sphere pinned by its farthest node; the
correct meaning of "on the surface" is equidistant.)

Bounded, no runaway cascade:

- Move a surface node X of center C → set R = dist(C, X) → scale C's other surface
  nodes to R along their own directions. Applied ONCE for X's centers; siblings are
  moved directly, NOT re-recursed through RootMove.
- Each moved sibling's OWN sphere R is still derive-on-read (recomputed when drawn),
  so it grows around its nodes without moving them. The 8↔1 feedback ring therefore
  cannot loop: coupling acts one level (C's surface), never recursing into the
  siblings' spheres.

Chord lock composes on top: after radial coupling, a chord-locked follower (node 6)
is set to mirror_φ(leader) (node 2) — same R, mirrored angle.

## 8. What this replaces

- The current Cartesian `meta.json` node layout (`x/y/z` per node, non-rooted
  radial-scale drag). The `xLock` group experiment and the circle-lock
  exploration are subsumed: a "lock" becomes a relationship expressed in the
  natural polar frame plus links, not a special-case Cartesian post-pass.

## 9. Open items (to resolve before/while implementing)

- ~~**Authoritative store format**~~ — RESOLVED (§8a): runtime polar; saved
  files Cartesian in the prism frame; prism = bounding box of current points,
  center = origin.
- ~~**Migration**~~ — RESOLVED (§8b): roots-first bootstrap from current
  Cartesian.
- ~~**Link representation in Go**~~ — RESOLVED (§8c): derive-on-read
  (Option A); roots are the sole authority; surface coords + sphere R are
  computed lazily. Soft membership — sphere R grows around its nodes, never
  pins them; a move touches one root, no cascade.
- ~~**Pole convention (Q9)**~~ — RESOLVED: pole = **+y (vertical)** for the
  container and every node's local frame.
- ~~**Bridge / what Go emits (Q5)**~~ — RESOLVED: Go owns **both**
  representations, but the **render bridge carries Cartesian** (Go derives it
  from its polar model and emits render-ready positions/normals). Polar stays
  **internal to Go** (the authoritative model + the save-file conversion, §8a)
  and never crosses to the renderer. This keeps `pump.ts` a **pure plotter that
  computes nothing** — the strict reading of the drift rule (if pump did the
  polar→Cartesian conversion, pump would be computing positions = drift).
- ~~**Mouse → polar (Q8)**~~ — RESOLVED: the drag is **sent to Go raw**
  (Cartesian cursor hit), fire-and-forget like every other edit; **Go converts
  it to the polar root update**. No conversion on the TS side.
- ~~**Camera model (Q7)**~~ — RESOLVED in direction: **replace** the current
  OrbitControls / `interaction-controls.ts` with the new camera-navigation
  system (pan = 2D Cartesian, zoom = Cartesian, rotate = polar orbit; camera
  on/inside the large sphere). Detail TBD against `camera-navigation.html`.
- ~~**Local pole orientation (Q10)**~~ — RESOLVED: each node's local frame uses
  the **large sphere's axis (world +y)** as its pole. The world/spheres do
  **not** rotate — only the camera rotates — so node polar coords live in a
  fixed world frame and camera motion never changes them.
- ~~**Sphere R definition (Q11)**~~ — RESOLVED: **R = max distance from the
  center to any of its surface nodes** (reach radius — farthest surface node on
  the ring, rest inside). Computed from positions (each node's root → point →
  distance), never stored on any root.
- **Tori / sphere rendering:** each node's sphere (and its great-circle tori)
  derived from its surface set; one source of truth (Go), rendered by TS.
