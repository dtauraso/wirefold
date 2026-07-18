---
branch: task/rotating-pole-frame
---

# Rotating per-node local pole (kick-on-threshold)

## Problem

The local-polar offset a node stores for each doubly-linked neighbor is a quantized
`(θ,φ,R)` about a pole. With the pole fixed at world +y, an offset whose direction
passes near +y hits the azimuth singularity: one φ-cell spans `R·sin(θ)·stepφ → 0`, so a
fixed world nudge crosses unbounded φ-cells and the quantized bearing becomes garbage.

## Model (agreed)

Keep the quantized `(θ,φ,R)` polar system. Make the **pole per-node and movable** so the
offsets a node quantizes never sit near its pole.

- **Per-node local pole** — each node owns one pole, a direction stored in polar
  (`dir{Theta,Phi}`), persisted in its `meta.json`. Offsets to that node's neighbors are
  quantized in the frame *poled at this pole*, via `spherical.go`:
  `c, psi = azimuthFrom(pole, offsetDir)` → `QuantITheta = Round(c/stepθ)`,
  `QuantIPhi = Round(psi/stepφ)`, `QuantIR = Round(R/stepR)`. (`c` = colatitude = angle
  from the pole; `psi` = bearing about the pole. The singularity is now at `c≈0`, i.e. an
  offset near THIS node's pole — a spot we control.)
- **Kick on threshold** — threshold `POLE_KICK_THETA = π/9` (20°). When re-quantizing on a
  drag, if any offset's colatitude `c` about the pole falls below 20°, **move the pole in
  polar** away from that offset: rotate the pole along the great circle through
  `(offset, pole)` until that offset sits at the equator (`c = π/2`), using
  `rotateDir(pole, arcBetween(offsetDir, pole).Axis, π/2 − c)`. Rationale for 20°: a φ-cell
  at 20° is still ~0.34× the equatorial width; below it the bearing degrades fast toward the
  blow-up — close enough to matter, far enough not to thrash the pole each drag.
- **Recalc as the pole moves** — a kick moves the pole, so ALL of that node's offsets are
  re-quantized about the new pole (intrinsic to the model, not a bolt-on). After a kick,
  re-check every offset; if a second offset is now < 20°, kick again (bounded: real nodes
  have 2–3 neighbors; a regression test asserts no oscillation on the live topology).
- **Persistence / respawn** — the pole is **persisted** per node (`localPoleTheta`,
  `localPolePhi` in `meta.json`), so it survives respawn verbatim (no need to be a pure
  function of geometry — the kick history is path-dependent, so we store the result). When
  a node has no persisted pole yet, initialize it perpendicular to the mean offset
  direction (mean offset parked at the equator), then let the first re-quantize apply any
  kicks and persist.

## Why this is a system fix, not a patch

The pole is a first-class movable frame, not a special case. Every offset quantization
goes through `azimuthFrom(pole, …)`; the ONLY change from a fixed pole is that `pole` is a
per-node stored direction that dodges its own offsets. No offset direction is ever
reconstructed into a node position (the cascade is distance-driven), so there is no
coordinate contention — the position blow-up class stays unrepresentable.

## Build surface

- `spherical.go` — reuse `azimuthFrom` / `fromAxisFrame` / `rotateDir` / `arcBetween` /
  `angularDistance` as-is (division-free, pole-parameterized).
- `layout_holder.go` — add `LocalPole dir` to the holder; the `LocalPolar` entries keep
  `(QuantITheta,QuantIPhi,QuantIR,steps)` but now mean "about `LocalPole`".
- The four re-quantize sites that today call `cart2polar(offset)` →
  `Round(θ/st),Round(φ/sp)` (loader.go `computeLocalPolars`; node_move.go
  `equalizeEdgeCLocal`, `moveNodeAndSetEdgeCs.setBothEnds`, `requantizeLocalPolars`) route
  through a single new `requantizeLocalPolarsAboutPole(nodeID)` helper that: reads all
  offsets, checks/kicks the pole, quantizes all offsets about the (possibly kicked) pole,
  persists pole + offsets.
- Persistence — `specNode`/`jsonMeta` (loader.go, loader_tree.go) + `writeQuantOffset` /
  a pole writer carry `localPoleTheta`/`localPolePhi`.
- Tests — (1) drag a node so an offset sweeps through the pole; assert the pole kicks and
  the quantized bearing stays bounded (fails on a fixed +y pole). (2) live-topology drag:
  assert no kick oscillation and the pole persists+reloads.
