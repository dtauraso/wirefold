---
branch: task/editor-3d-plan
---

# 3D Editor Plan

## Motivation

The goal is **not** cosmetic depth. The topology genuinely has depth: the wire
structures described in MODEL.md (inhibitor chain, rings, lateral-inhibition
lattices) have real geometry that the current 2D React Flow canvas flattens into
misleading edge crossings and false adjacencies. Moving to 3D is about
**representational honesty** — making the rendered structure match the actual
structure.

## Medium vs. substance (per CLAUDE.md)

Going 3D is almost entirely a **medium** change. The following stay **untouched**:

- Go substrate (`nodes/`, `Wire.go`, `nodes/Wiring/loader.go`, `nodes/Wiring/builders.go`)
- Substrate model (slot-phase, AND-gate tree, backpressure)
- `pump.ts` firing and animation logic

Only rendering, interaction, layout, and the position schema change.

**Drift signal:** if 3D math starts appearing in `pump.ts` or the Go substrate,
stop — that is the wrong frame.

## Coordinate model — derived, not authored

Today, node positions are hand-laid `x/y` in `topology.json`; ports carry a
`side` (top/bottom/left/right) + `slot`.

**Key decision:** in 3D, coordinates are **derived from connectivity**, not
hand-placed. A ring of chain-inhibitors is a circle; stacked rings step along
an axis; a lateral-inhibition lattice is a grid. Layout is a deterministic
**structure → coordinate function** over the wiring graph — not force-directed
guessing and not manual dragging. Position *means* something.

Schema changes required:

- Add `z` to the node position model in `parse-spec.ts`, the flow adapters, and
  `topology.json`.
- Replace the 2D port-anchor model (`side` + `slot`) with a 3D port-anchor
  model (a vector offset from the node center).

This is the substance-adjacent piece that delivers the value.

## Interaction grammar

The cursor is always a **screen-space x,y pointer** — it never acquires a depth
coordinate.

### Three non-overlapping input channels

| Channel | Gesture | Effect |
|---------|---------|--------|
| 1 | **Click** | Raycast pick — select a node or pick a port endpoint for wiring |
| 2 | **Drag** | Arcball rotation of the structure — axis perpendicular to the local drag tangent, incremental quaternion composition |
| 3 | **Scroll (default)** | Dolly camera through depth (z-axis) |

### Rotation detail

A drag is a **rotation**: the drag's screen direction sets the axis (perpendicular
to the drag direction). A curved drag is a continuously-changing rotation —
per-frame quaternion increments are composed about the axis perpendicular to the
**local** drag tangent, so curving the drag sweeps the axis continuously. This
falls out for free from incremental composition; do **not** map the whole drag
arc to one fixed axis.

### Wiring

Because drag is spent on rotation, **wiring is not a drag**. Connect = click
port A, click port B (two screen-space raycast picks); layout recomputes from
the new connectivity. This dissolves the hardest unsolved problem of 3D graph
editors (cursor-to-3D projection for drag-connect) by eliminating the
projection entirely.

## Rendering stack

React Flow is fundamentally 2D and load-bearing everywhere (node registry,
`SubstrateEdge.tsx`, RF store, pan/zoom/select/connect). 3D means **replacing**
it with the converged-on React 3D stack:

> **react-three-fiber + three.js — no drei**

R3F is the non-weird medium choice — what the rest of the world converged on
for React + 3D. drei is explicitly excluded (see Medium stack section below).

### Component mapping

| Current (2D) | 3D equivalent |
|---|---|
| `<Kind>Node.tsx` custom node | Mesh + HTML/DOM overlay (React div or CSS2DRenderer) |
| Validation flag + pseudo panels | Projected/billboarded overlays |
| `SubstrateEdge.tsx` 2D path | 3D spline/tube geometry |
| Pulse animation (pump-driven) | Same pump logic; geometry it travels changes from 2D SVG path to 3D curve |

The **"render only, no substrate logic"** contract on `<Kind>Node.tsx` is
unchanged. The pump-driven pulse animation logic stays in `pump.ts`; only the
geometry it travels over changes.

## Open questions (to resolve with David before building)

- **What rotates?** Whole scene (arcball about scene center — assumed default)
  or a selected sub-cluster (separate mode)?
- **z-dolly trigger:** scroll wheel assumed — confirm the input is available in
  the chosen react-three-fiber setup.

## Problem #1 (DOF mismatch) — resolved control scheme

3D navigation needs 6 degrees of freedom (translate X/Y/Z plus three rotations).
An input device may provide fewer. Without a principled mapping, operations must
share inputs and disambiguation requires modes — hidden state that causes errors.

### Governing axiom: never sacrifice control

This is **not** a second rule competing with CLAUDE.md's medium-vs-substance
rule. Two rulebooks force adjudication, and adjudication is where drift toward
the industry default slips in. The axiom is a **classification clause** of the
one existing rule — it tells you which side of the line 3D interaction falls on.

**The medium-vs-substance rule, applied here:**

- **RENDERING / PLUMBING = medium.** Three.js + react-three-fiber as the render
  substrate, quaternion math, bundler, file watcher — adopt the dominant choice,
  no hesitation. R3F is the correct, non-weird medium pick. The axiom never
  touches these.
- **CONTROL OVER THE SYSTEM = substance.** Which DOF exist, that all stay
  directly controllable, never sacrifice control — design from need, ignore
  industry. Same category as "what a node is" or "how ticks work."

**The trap, named explicitly.** `drei`'s `OrbitControls` is a substance decision
(a control scheme) shipped inside a medium library (R3F/drei). That packaging is
why it gets mistaken for medium and pattern-matched in. "Adopt R3F" (medium, yes)
does **not** imply "adopt OrbitControls" (substance, no).

**The single, competition-free decision procedure:**

1. Is this rendering/plumbing, or control over the system?
2. Rendering → industry default (R3F: yes).
3. Control → substance → design from need → apply the recoverable-by-device test.

**Recoverable-by-device test (operative drift-guard).** If a better input device
does NOT restore a lost capability without changing the design, the loss is baked
into the design — it is a wrong industry pattern-match → REJECT it. Click-tricks
pass: a 6-DOF device restores simultaneity without any design change. OrbitControls
fails: a SpaceMouse still leaves a fixed pivot and locked roll — the loss is in
the design.

There is now ONE rule. The medium-vs-substance rule, correctly applied, rejects
OrbitControls on its own. The axiom isn't overriding it — it's marking where the
line is.

### The design vs. the input fallbacks

**THE DESIGN (device-independent).** All six DOF are directly controllable. Drag
= content-pivoted, path-integrated rotation about the picked point. The
remaining DOF are each independently addressable. This is the invariant; it does
not change with the input device.

**NATIVE TARGET — 6-DOF input (SpaceMouse-class).** On it every click-trick
disappears: no dwell, no mode discrimination, no summoned pad. Push/twist gives
translation + rotation simultaneously, full control, zero sacrifice. This is the
version that is "just fine" — the design expressed without contortion.

**GOOD FALLBACK — trackpad multitouch.** A trackpad is not a 2D mouse. Two-finger
pan, pinch dolly, and two-finger twist (roll) are real extra channels that replace
the most awkward click-tricks with direct gestures, no control sacrifice. It sits
between the bare mouse and the 6-DOF puck.

**LAST-RESORT FALLBACK — bare 2D mouse.** The single-button click-tricks (dwell
to summon a floating pan pad, movement-vs-dwell gesture discrimination, ~200 ms
timings) exist **only** to fake DOF out of a 2D device. They are a degraded
fallback, not the interaction model. The awkwardness they carry is correctly
located in the **device**, not the design — the design refuses to paper over a
2D input by stealing control, unlike the conventions, which paper over it by
stealing control.

### The design: rotation rule and control table

*The following describes the device-independent design. On a 6-DOF puck the
table collapses to "push/twist"; on trackpad two-finger gestures cover pan and
dolly directly. The table as written is the explicit per-DOF mapping exposed
when all else degrades to a bare mouse.*

**Rotation rule.** Pivot = the clicked point at its depth `(x_picked, y_picked, z)`.
Rotation is computed per successive pair of points in the drag: each segment
defines a line, the rotation axis lies in the screen plane perpendicular to that
segment, the slope sign gives direction, and magnitude is proportional to segment
length. Increments compose as quaternion products.

Consequences:

- A straight drag = constant axis (one rotational DOF swept).
- A curved drag = axis sweeps continuously (two rotational DOF mixed naturally).
- Total rotation accumulates along the drag's **arc length** — a wiggly path
  adds rotation even if the endpoints are nearby.
- Two of the three rotational DOF are reachable from any drag. The one rotation a
  drag *cannot* directly produce is spin about the line of sight (screen-plane
  twist/roll); that gets its own dedicated control.

**Control table.**

| DOF | Control |
|---|---|
| Rotate (drag direction A) | pointer drag |
| Rotate (drag direction B) | pointer drag |
| Rotate (screen-plane spin / roll about line of sight) | scale / slider widget |
| Translate X | pan pad |
| Translate Y | pan pad |
| Translate Z | ^/v button (hold to dolly) |

**Open conventions (not blockers):**

- Which sign `^` is: dolly toward the scene or dolly away.
- Empty-space pivot: what a drag rotates about when no item was clicked at
  mouse-down. Options: disable rotation entirely; fall back to scene center;
  fall back to a fixed depth.

### Last-resort 2D-mouse fallback: gesture discrimination and timings

*This section applies only when the input is a bare 2D mouse. On trackpad, the
two-finger channels replace the tricks below. On a 6-DOF puck, none of this is
needed.*

The pan pad is a FLOATING control summoned under the cursor (like a mobile
floating joystick), not a fixed widget. Three actions share a single
pointer-down, discriminated by movement and time-held:

| You do | Resolves to |
|---|---|
| press → move beyond a small slop | drag → rotate |
| press → release quickly, no move | click → pick the item |
| press → hold still a very short beat | pan pad summons at cursor; then move = pan |

**Discrimination rule: movement-first wins.** Any motion past the slop
threshold (~4–8 px) commits to rotate and cancels the pad even mid-dwell, so
only a truly stationary hold summons the pad. This protects the hesitant user
(press, pause, then drag to rotate) from accidentally summoning the pad.

**Recommended timings** (with rationale grounded in perception thresholds):

- **Normal click (pick):** release under ~150 ms with no move. (~100 ms is the
  "instantaneous" perceptual floor; a click lands here.)
- **Pan-pad dwell:** ~200 ms stationary hold. Above the ~150 ms click-release
  floor so it reliably distinguishes from a tap; below the ~300 ms "consciously
  perceptible pause" line so it feels like a brief settle, not a wait; well
  under the 500 ms OS long-press standard (Android `getLongPressTimeout` / iOS
  `minimumPressDuration`) so it won't collide with long-press muscle memory.
- **Movement slop:** ~4–8 px overrides the dwell timer.

**Touch caveat:** ~200 ms is too short for touch (finger jitter, slower taps);
on touch the comfortable long-press is ~400–500 ms. If the editor ever targets
touch, make the dwell device-adaptive: ~200 ms mouse, ~500 ms touch.

### Why the conventions sacrifice control (medium comparison)

Per the decision procedure above, interaction splits: rendering is medium (adopt
R3F), control is substance (design from need). The five families, examined
through that lens — specifically the recoverable-by-device test:

1. **Orbit/pan/zoom** (Three.js `OrbitControls`, Google Earth, most web and CAD
   viewers): orbit = left-drag (yaw + pitch, up vector **locked**, no roll);
   pan = right-drag or Shift+drag; zoom = scroll wheel. Sacrifices roll and
   movable pivot to make a 2D mouse feel smooth. Fails the axiom.

2. **Trackball / arcball**: drag → rotation via a virtual sphere; free tumble
   including roll, no locked-up vector. Our drag rule is an arcball variant; the
   distinctive part is pivoting about the *picked point* ("rotate about cursor"
   in some CAD tools). Does not sacrifice rotational DOF — closest to the axiom
   among drag-only approaches.

3. **Modifier/button chords** (Blender MMB / Shift+MMB / Ctrl+MMB; Maya
   Alt+L/M/R): same drag, meaning set by button or key held plus scroll wheel.
   Hides modes; fails the axiom's "no hidden state" corollary. The SpaceMouse
   adoption rate in Blender/Maya users is the market verdict.

4. **Dedicated 6-DOF hardware** (3Dconnexion SpaceMouse): all 6 DOF
   simultaneously via a puck. This is the native target — the axiom is trivially
   satisfied because the input matches the DOF count.

5. **Touch / on-screen controls**: one-finger orbit, two-finger pan, pinch
   zoom, two-finger twist for roll; or on-screen joysticks, gizmos, ViewCube
   (mobile, kiosk, games). The trackpad variant of this is the good fallback;
   on-screen-only widgets are the 2D-mouse fallback generalized.

**Where our scheme lands.** Rotation = family 2 (arcball; free tumble is the
benefit over locked orbit, and it does not sacrifice DOF — recoverable-by-device
test passes). The pan-pad + z-buttons + roll-slider for the 2D-mouse fallback
sit closest to family 5. Correct location: degraded-device scaffolding, not the
design. Families 1 and 3 fail the recoverable-by-device test; family 4 is the
native target where the test is trivially satisfied.

## Problem #2 (depth ambiguity) — resolved

Depth is resolved by two layers: active motion (already present in the design)
plus the standard 3D graph-viz rendering convention (adopted as MEDIUM — it
sacrifices no control and passes the recoverable-by-device test trivially, unlike
OrbitControls).

### Active motion (primary — already in the design)

- **^/v dolly:** under perspective, near nodes change size and sweep past faster
  than far ones — the rate difference reveals relative depth.
- **Rotation drag:** lateral parallax — different depths shift sideways at
  different rates.

Depth comes from **control**: the user probes it. Consequence: depth is
**active, not passive** — a frozen frame needs a nudge to read depth.
Acceptable, because derived layout already conveys structure.

### The usual way (passive cues — adopted as pure medium, no control sacrifice)

Decouple the node body from its label:

- **Node BODY = a 3D primitive.** Sphere is the graph-viz default: orientation-
  invariant, no billboard decision required, shades for depth, occludes
  correctly. For wirefold's richer nodes (ports, kind, pseudo-text), use a
  small 3D body (sphere or rounded solid) carrying kind and ports rather than a
  bare dot.
- **LABEL = a separate billboarded text** that always faces the camera, shown on
  hover or for nearest/selected nodes (level-of-detail) to cut clutter.

Key point: depth cue lives in the 3D body; legibility lives in the billboarded
label. Not an either/or — both, decoupled. (This also pre-answers part of
Problem #7.)

### Depth cues, ordered by strength

1. **Occlusion** — strongest, free from the depth buffer.
2. **Motion parallax** — dolly + rotation.
3. **Relative size** — perspective falloff, free.
4. **Shading** — lighting across the 3D body.
5. **Depth fog / desaturation** — optional, for dense graphs only.

### Explicitly not used

**Color-for-depth** and **shape-for-depth** are both rejected. Color and shape
are reserved for DATA: kind, validation flag, port/wire kind, pulses. Mapping
depth onto those channels would collide with semantics — a sacrifice, rejected.

## Problem #3 (occlusion) — resolved

### Governing rule: full occlusion is allowed

**Full occlusion is ALLOWED.** A node may go 0% visible behind a nearer node.
This keeps occlusion as the strongest depth cue (#1 in Problem #2) fully intact
and — critically — nothing moves, so the layout never lies (honesty axiom
preserved). This reverses and supersedes the earlier outline/silhouette rule.

Recovery of a hidden node uses the same camera gestures and buttons from
Problem #1 (rotate, dolly, pan to move the viewpoint). No transparency, no
relocation, no shadow/fade.

### Discovery signal: count badge on the front node

The one gap full-occlusion allows is **discovery loss** — a node the user has
never seen because it is always behind another. That gap is closed by a **count
badge** on the visible front node:

- A small numeric badge (e.g. "+3") appears on the front node showing how many
  nodes are hidden directly behind it from the current viewpoint.
- The badge reads as "there are nodes back here — rotate to reveal them."

No transparency, no outline-through-walls, no structure alteration. The front
node stays solid; the badge carries the count.

### Why badge over edge thickness

A badge is **absolute and self-describing** — "+3" reads at a glance without
comparing it against neighboring edges. Edge thickness is only comparative: the
user must infer the count by judging relative widths, and that channel collides
with the conventional meaning of edge width (data weight, flow capacity). Keeping
thickness free for data is consistent with Problem #2 reserving color and shape
for DATA (kind, validation flag, port/wire kind, pulses) — the same principle
applied to the edge-width channel.

### Industry grounding

Allowing full occlusion plus camera-motion recovery is the empirically supported
mainstream (Ware & Franck). For signalling "how many nodes are hidden here,"
large-graph tools — Gephi, Cytoscape, and most network UIs — use a count badge
or "+N" aggregation marker; that is the conventional pick for this slot.

The **HALO** technique (depth-halo / silhouette-through-walls; Interrante /
Everts) was considered and not chosen. A HALO shows the hidden item's actual
shape through the occluding surface, which conveys more detail — but it adds
outline clutter and more visual weight than is needed when the only question is
"is something back there, and how many?" A badge answers that question without
drawing into the occluded region.

### Timing: post-gesture, not per-frame

Badge counts are recomputed **after a gesture settles**, not continuously during
drag or rotate. No per-frame cost, no flicker mid-motion.

### Open conventions (not blockers)

- **Edge-on-node occlusion.** Does an edge (rather than a node) occluding a
  node count toward the badge number?
- **Badge placement.** Where does the badge anchor when the front node itself is
  partly occluded?
- **Large-count format.** Threshold and display format for large behind-counts
  (e.g. "+99+" cap).

## Problem #4 (port picking) — resolved

### Reframe: React Flow plus a Z axis

Going 3D is **adding a dimension, nothing more.** The reframe bounds the whole
problem: React Flow already models ports as handles; each edge already carries
`sourceHandle`/`targetHandle` — the logical channel between nodes. That structure
is unchanged. The question "how does port picking work in 3D?" dissolves once the
frame is correct.

### Medium stack: renderer vs. state vs. logic

The "RF plus a Z axis" reframe clarifies which pieces RF was actually supplying
and what happens to each:

- **Renderer → R3F + three.js only — no drei.** React Flow is a 2D library; it
  goes away. R3F (react-three-fiber) is the React renderer for Three.js — it is
  ONLY a 3D scene renderer (meshes, camera, lights). It is **not** a
  graph/node-editor library. RF had given us nodes, edges, handles, and connection
  logic for free; no turnkey "React Flow in 3D" exists, so that graph/edge/handle
  model is now ours to maintain on top of R3F. The data shape we called "the RF
  model" survives — RF the library no longer supplies it. (Medium — adopt the
  industry default.)

  **Why no drei:** drei's headline feature is OrbitControls, which Problem #1
  explicitly rejects on substance grounds — we build custom controls regardless,
  so drei's main draw is dead weight. drei's `<Text>` wraps `troika-three-text`,
  the exact source of the CSP/worker/blob watch-item below. Per Problems #2/#7,
  labels are billboarded + LOD'd and render as **HTML/DOM overlays** (React divs
  over the canvas, or three's CSS2DRenderer) — not in-scene SDF text. This gives
  crisper text, easier styling for the red validation flags, and eliminates the
  troika CSP risk entirely. The few remaining drei conveniences (Billboard,
  fat Line, Html) are small: fat lines via three's own `Line2`/`LineMaterial`;
  billboarding via a few lines in a frame callback. Modest cost; avoids drei's
  whole transitive tree.

- **Graph state → Zustand stays.** RF's store was Zustand under the hood, but
  Zustand is standalone with no RF dependency. When RF goes away, Zustand remains
  the home for graph state (nodes, edges, `sourceHandle`/`targetHandle`,
  selection). (Medium — adopt the default; do not hand-roll a store.)

- **Graph logic → ours (substance).** Connection rules, what a wire means, the 3D
  path optimizer — these are not supplied by any medium library and were never
  supplied by RF. We own them regardless of renderer choice.

**Security note.** R3F core + three.js core are low-surface — no worker/blob
paths. Dropping drei/troika **resolves** the former CSP watch-item: there is no
longer a troika worker/blob path that could force a VS Code webview CSP
relaxation. Hygiene going forward: pin the lockfile and run `npm audit` when any
new dependency is added.

### Three layers, kept strictly separate

1. **Logical connection (Go substrate).** Output channel of node A to input
   channel of node B — the truth. No geometry, no position.
2. **Human-speed timing / pulse-animation layer (pump-driven).** Bridges logical
   events to what a human sees. Geography-free.
3. **Geographical connection (rendered 3D wire/path).** The spline or tube that
   represents the wire in space, including where it exits/enters the node body.
   This is a visual/path concern only.

The logical port (layer 1) is decided by what RF already does — `sourceHandle`/
`targetHandle` on the edge. The optimizer-picked exit point (layer 3) never
decides the logical port. The layers are distinct.

### What the extra dimension actually changes

The extra dimension changes **geography only** — rendering. In 3D, a port handle
may be tiny, occluded, or facing away; you cannot reliably aim at it. The
resolution: **you click the NODE**, not the port handle. The wire takes the most
efficient 3D path between the two nodes, exiting and entering the node bodies
wherever the path optimizer picks. That is a layer-3 decision. The
`sourceHandle`/`targetHandle` (layer 1) is not a geographical decision and
requires no new disambiguation step.

**Sole delta from React Flow:** the assignment target. In RF you drag from a
specific port handle; in 3D you click the node. That one change — assign to node
instead of drag from handle — is the only difference. The logical handle/channel
model, the edge data (`sourceHandle`/`targetHandle`), and the timing/pulse-
animation layer are all identical to what RF already has. The node-level
assignment is precisely what hands the geographical exit/entry point to the path
optimizer.

### Why the earlier framing was wrong

Earlier framing tried to force the logical-port choice into the geographical
click — treating "which exit point did the cursor aim at?" as the mechanism for
port assignment. That conflated layer 1 with layer 3. The logical port is not a
geographical decision; RF already handles it. There is no new port-picking UI to
design.

## Next concrete step

Build a **throwaway react-three-fiber prototype** that validates the gesture
grammar only:

- Static cluster of cubes (no real topology, no substrate)
- Arcball drag-rotate via incremental quaternion composition
- Scroll-dolly camera
- Click-to-pick (highlight a cube)

No schema work, no layout derivation, no pulse animation, no node registry
changes. The prototype exists solely to confirm the interaction **feels right**
before any schema/layout/renderer-integration work begins.
