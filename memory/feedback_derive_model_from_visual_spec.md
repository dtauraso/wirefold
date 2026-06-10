---
name: Derive the model from the visual spec before patching
description: When David describes visual/behavioral contracts, derive the implied Go model up front and surface it — do not build the smallest patch that satisfies today's symptom.
type: feedback
originSessionId: cb9eb81d-c150-4951-adcd-4263a93d46d1
---
When David specifies visuals or behavior ("the pulse travels the wire, length/shape don't matter, edits don't break it, one tick"), that IS the system spec. The implied Go model falls out of the constraints almost mechanically. Derive it explicitly and present it before writing code. Refuse cheap local patches that satisfy the current symptom while preserving a model the next constraint will break.

**Why:** This project has gone through 5–7 Go rewrites (legacy sim → wires → ack-driven → andGateLoop → step-function → pair → ticked → Go-owned ticking). Each rewrite fixed runner/node semantics and left the same underlying gap untouched (notably: wire is not a first-class entity). David paid for the same problem repeatedly because each session optimized for "smallest diff that makes today's visual pass" rather than "build the model the visual contract implies." His visual specs were clear and sufficient; the implementer side kept laundering them through handoffs into cheap-fix language ("defer ReadGate to next tick" instead of "wires are entities") and the model wrongness surfaced only after multiple patches had fused around it.

**How to apply:**
- When David states a visual/behavioral contract, before proposing implementation: write down what entities, states, invariants, and boundaries the contract implies. Present that derivation.
- If the honest implementation is large, say so plainly. Do not substitute a near-miss patch and hope the gap doesn't surface.
- Treat "small diff, low risk" as a tell, not a virtue, when it preserves a model the spec contradicts. Cheap fixes on a wrong model compound; one model fix is cheaper than N rewrites around it.
- **Concrete numbers are instances of a model, not the model itself.** When David says "make there be 2 slots" for a node already labeled "AND", the model is N-slot AND — the 2 is today's instance. Hardcoding 2 in the kind defaults instead of adding per-instance arity is the same anti-pattern. Read the surrounding labels/comments/CLAUDE.md to recover the model before picking the diff size. (Wirefold case 2026-05-13: hardcoded `ReadGate.inputs = [chainIn, chainIn2]` after relaxing the Go side to N≥1, then had to redo it as a per-instance `Node.inputs?` override.)
- Do not let prior handoffs override David's stated spec. Handoffs can launder specs into cheap-fix framings; check the current framing against what David actually said, not against the inherited vocabulary.
- When a category of bug recurs (same shape, different layer), that is the signal the model is wrong — stop patching, name the model gap, propose the model change.
- Specifically for wirefold: wire is not yet a first-class entity. Any feature that needs a wire to hold state, delay, signal occupancy, or own a pulse should be flagged as "this implies wire-as-runner; the cheap alternative is debt." Do not silently route wire behavior through node inboxes, renderer animation, or pubsub side channels.
