---
name: project_wire_is_straight_line_not_chain
description: Wires are straight-line PacedWire segments; the bead-item chain model was explored and rejected for O(N²) follow latency.
metadata:
  type: project
---

Wires render as straight `wireSegment` / `lerp` lines drawn by `PacedWire` + `SingleEdgeTube`, with one moving `PulseBead`. A "bead-item chain" wire model (a wire as N bead-sized goroutines that relax to straight via midpoint-of-neighbors, born/retired to hold spacing, the pulse hopping item-to-item) was fully built and then **reverted** (revert commit `e7faf250`; MODEL.md chain section dropped in `45521898`).

**Why rejected:** straightness is an endpoint-defined (non-local) property. Neighbor-only midpoint relaxation is discrete diffusion — the chain-spanning bend is the slowest Jacobi mode, decaying only ~1/N² per propagation step, so a node-drag takes O(N²) latency to follow (measured ~1.5s at N≈40 beads). Going async/goroutines does NOT help: parallelism removes the per-round N-beads factor, but the N² propagation steps form a sequential dependency chain (each step's input is the previous step's output) and can't be parallelized. The only escape is giving each bead the two anchors directly (lerp on the anchor line → dependency depth 1), which means broadcasting endpoint data to every bead.

**Key distinction David drew:** neighbor-to-neighbor messaging (local, bounded fan-out — it's CSP/actors) is NOT the thing to avoid; GLOBAL scope (one place knowing all nodes, or all-to-all) is. A single `lerp` is cheaper for a straight wire, and *drawing* is a render concern, not the computational substance.

Don't re-propose the bead-chain wire model for straight wires. Two fixes from the detour were kept: node body follows local React Flow position (drag fix), and the straight `wireSegment`/`lerp` in PacedWire. See [[feedback_uniform_pulse_speed]] and [[feedback_ease_of_fix_is_confounded]].
