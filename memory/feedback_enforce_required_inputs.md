---
name: Propose robust solutions that fit the model spec
description: The editor does NOT visually flag missing required inputs (red node / parseSpec diagnostic removed 2026-06-01). The substrate does not reject graphs with missing required inputs. Precondition-gating keeps unfed nodes inert.
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

**UPDATED 2026-06-01** — the render-only validation-flag editor feature was removed
(merge of task/remove-validation-flag). `requiredInputDiagnostics()` and all red-node
rendering are deleted from the codebase.

**Current rule:**

- Missing required wire → **no editor flag**. The node stays in the graph, unremoved,
  and the editor does not mark it red or emit a parseSpec diagnostic.
- Pressing Run executes all nodes; a node whose required slot never fills is naturally
  inert (precondition-gating keeps it from firing).
- Do **not** add a fatal `validateSpec`/`parseSpec` reject for a missing required wire.
- The substrate remains lenient at load; there is no visual "is this complete?" signal
  in the editor for missing required wires.

**How to apply:** when designing a feature whose correctness depends on a wire being
present, do not add a `parseSpec` diagnostic or red-node rendering — that path is
gone. Trust precondition-gating to keep the unfed node inert at run time.

Related: [[feedback_substrate_vs_coordinator_bias]] (find the local signal, not a
coordinator-shaped reject), [[feedback_derive_model_from_visual_spec]] (derive the
model up front from the visual spec).
