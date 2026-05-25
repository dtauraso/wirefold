---
name: Propose robust solutions that fit the model spec
description: When a node's correctness depends on a wire being present, flag its absence in the editor (parseSpec diagnostic → red node); do not add a fatal validateSpec/parseSpec reject. Let precondition-gating keep the node inert.
type: feedback
originSessionId: 96608f66-f9a2-47a3-bd1f-48841e9eb98a
---
**REVERSED 2026-05-25** — the prior posture (fail parseSpec / reject the graph) was demoted.

`nodes/Wiring/validate.go` Check 4 (missing required inbound edge) was demoted to
non-fatal in commit 0e8d843. The substrate no longer rejects a topology for a missing
required inbound edge.

**Why the reversal:** nodes are precondition-gated and self-scheduling. A node whose
required slot never fills is naturally inert — it polls, finds the slot empty, and
returns without firing. It does not need to be rejected or removed. The runnable subset
of the graph runs; the unfed node just never fires. Enforce-by-reject made one
incomplete node kill the entire graph load; precondition-gating already gives the
desired "don't run it" behavior without removal.

**Current rule:**

- Missing required wire → **editor flag** (parseSpec diagnostic → visual red mark on
  the node). The node stays in the graph, unremoved.
- Pressing Run executes all non-flagged nodes; flagged nodes are inert (precondition
  never satisfied).
- Do **not** add a fatal `validateSpec`/`parseSpec` reject for a missing required wire.
- Keep substrate lenient at load; push the "is this complete?" signal to the editor
  surface.

**How to apply:** when designing a feature whose correctness depends on a wire being
present, add a `parseSpec` diagnostic that marks the node red in the editor. Do not
add a substrate-level reject. Trust precondition-gating to keep the unfed node inert
at run time.

Related: [[feedback_substrate_vs_coordinator_bias]] (find the local signal, not a
coordinator-shaped reject), [[feedback_derive_model_from_visual_spec]] (derive the
model up front from the visual spec).
