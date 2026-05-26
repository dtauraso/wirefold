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

> **react-three-fiber + drei (Three.js)**

This is the non-weird medium choice — what the rest of the world converged on
for React + 3D.

### Component mapping

| Current (2D) | 3D equivalent |
|---|---|
| `<Kind>Node.tsx` custom node | Mesh or billboarded `<Html>` panel (drei) |
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

The cardinal rule: an interaction scheme must keep **all six DOF directly and
fully controllable**. Sacrificing control to make an input device feel fast or
smooth is the disqualifying flaw. Fluency is not worth control — being expert at
a tool that took control from you is still a loss.

**Why the conventions fail this.** The dominant families — orbit/pan/zoom,
modifier-chord schemes (OrbitControls, Blender, Maya, CAD) — sacrifice control:
fixed pivot instead of a movable one, locked "up" vector with no roll, hidden
modes, no visible model of state. The trade is made purely to make a 2D mouse
feel fluent. That trade is backwards.

**Evidence it was always a workaround, not a good design.** The 3Dconnexion
SpaceMouse exists. The same professionals who mastered Blender and Maya bought a
separate 6-DOF puck specifically to escape the chords — voting with money that
the 2D-input scheme was tolerated, not good. "Pros got fast" means "pros adapted
to bad software/input, and the ones who could afford it paid to get out."

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

Per CLAUDE.md's medium-vs-substance rule, interaction is **medium** — the
converged industry answer is the default unless a deviation is justified. The
five families, examined through the axiom:

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
benefit over locked orbit, and it does not sacrifice DOF). The pan-pad +
z-buttons + roll-slider for the 2D-mouse fallback sit closest to family 5. That
is the correct location: they are degraded-device scaffolding, not the design.

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
