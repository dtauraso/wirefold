---
name: feedback-uniform-pulse-speed
description: "Pulse speed is a global Go constant, not a per-wire property — reject per-wire `speed` props even when an analysis doc argues for them."
metadata: 
  node_type: memory
  type: feedback
  originSessionId: 643b8a26-d4bb-43c8-831e-5383e886d4bb
---

Pulse speed must be the same for all wires. Do not add a per-wire `speed` prop to `RWireSpec` (or equivalent) even if it looks like a clean win for expressiveness.

**Why:** the Go network's correctness rests on uniform pulse speed. `partitioned-launching-fog.md` §6 argued per-wire speed was "the strongest single argument for the control-flow model" because it would let "inhibit travel faster than data." David rejected this when implemented — variable wire speed is not a property the Go network should have. The control-flow model formalization still goes through without it (fan-out convergence + doc reframe stand alone).

**How to apply:** when reading `partitioned-launching-fog.md` or any future doc that proposes per-wire speed as a model feature, treat that section as invalidated. If a future task implies heterogeneous wire timing (e.g., "inhibit arrives first"), surface the constraint and ask for the topology-level mechanism (e.g., shorter inhibit path, upstream wiring) rather than adding a speed knob. See [[feedback-derive-model-from-visual-spec]] — David's specs are sufficient and propose-and-revert is more expensive than proposing the right thing the first time.
