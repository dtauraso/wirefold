---
name: feedback_abc_times_constant_not_rederive
description: Update polar positions as abc-index × step-constant (arithmetic), not by re-deriving with spherical trig; the only trig is the cartesian↔polar boundary
metadata:
  type: feedback
---

David built a 3-D teaching demo (`docs/demos/polar-drag-3d.html`) specifically to show the
update model for the polar layout — how to compute new coordinates WITHOUT the "fancy
formulas" (azimuthFrom / law-of-cosines / rotating-pole trig) I kept reaching for.

The model:
- A position/offset is stored as **abc = integer indices × step constants** (r-index·stepR,
  θ-index·stepθ, φ-index·stepφ). The abc's ARE the state.
- **Updating on a move is arithmetic on the indices** (± whole steps) — NOT re-projecting the
  offset through spherical trig each time.
- **Pole moves are fixed constant increments**: "N near the pole" is a constant compare
  (dot vs a precomputed `cos(kick)`, no `acos`); the tilt DIRECTION comes straight from the
  offset's cartesian components (`cross(offset,+y)`, no `atan2`); the tilt magnitude is a
  precomputed constant. Far → 0 change.
- **The ONLY trig allowed is the cartesian↔polar boundary** — reading the abc out of a
  cartesian offset once, and drawing abc back to pixels. Nothing rederives beyond that one
  conversion.

**Why:** the current Go code (`rotating_pole.go` `localPole`, `spherical.go` `azimuthFrom`)
rederives with trig on every requantize — the exact thing this model avoids. My default
instinct was to add more spherical-trig reprojection; the demo exists to correct that.

**How to apply:** when touching the polar-constant update path (`node_move.go` requantize,
`rotating_pole.go`, `layout_holder.go` local polars), keep offsets in **cartesian** as long
as possible, convert **once** at the boundary, update abc by **scalar × constant**, and move
the pole by **fixed increments** with direction taken from components. Do NOT reintroduce
per-update `azimuthFrom`/`rotateDir`/law-of-cosines rederivation. Note the Go layer is
angle-based by design (`spherical.go`: no vectors/quaternions), so applying this means moving
that work into cartesian — a MODEL.md-level change to verify against `rotating_pole_test.go`,
not a blind edit. Related: [[feedback_go_vs_coordinator_bias]], [[project_theta_phi_tilted_camera]],
[[project_torus_colinearity_future_equation]].
