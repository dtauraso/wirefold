---
name: feedback-uniform-pulse-speed
description: "Pulse speed is a global Go constant, not a per-wire property — reject per-wire `speed` props even when an analysis doc argues for them."
metadata: 
  node_type: memory
  type: feedback
  originSessionId: 643b8a26-d4bb-43c8-831e-5383e886d4bb
---

Pulse speed must be the same for all wires. Do not add a per-wire `speed` prop to any wire spec, even if it looks like a clean win for expressiveness. MODEL.md carries the live form of this — `ticksToCross = arcLength / pulseSpeed`, with `pulseSpeed` uniform.

**Why:** the Go network's correctness rests on uniform pulse speed. An analysis doc once argued per-wire speed was "the strongest single argument for the control-flow model" because it would let "inhibit travel faster than data." **David rejected this when implemented** — variable wire speed is not a property the Go network should have. The control-flow model formalization goes through without it.

**How to apply:** if a future task implies heterogeneous wire timing (e.g. "inhibit arrives first"), surface the constraint and ask for the topology-level mechanism — shorter inhibit path, upstream wiring — rather than adding a speed knob. Treat any doc section proposing per-wire speed as invalidated on sight. See [[feedback_derive_model_from_visual_spec]] — David's specs are sufficient, and propose-and-revert is more expensive than proposing the right thing the first time.

**Vocabulary note:** `RWireSpec` and the doc that made the argument (`partitioned-launching-fog.md`) are both gone. The rejection stands on David's call, not on that doc; nothing here needs it to exist.
