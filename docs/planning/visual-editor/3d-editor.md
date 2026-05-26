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
