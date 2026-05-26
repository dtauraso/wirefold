---
name: project-interaction-control-is-substance
description: 3D-editor interaction control is substance, not medium; the standard 3D-interaction default (OrbitControls) sacrifices control and is a wrong pattern-match
metadata:
  type: project
---

For the 3D visual editor, **interaction control is substance, not medium** — it is NOT a second rule competing with CLAUDE.md's medium-vs-substance rule, it is a classification clause of that one rule. Holding two rulebooks forces adjudication, and adjudication is where drift toward the industry default slips in.

**Governing axiom:** never sacrifice control. An interaction scheme must keep all six DOF directly and fully controllable. Trading control to make an input device feel fast/smooth is the disqualifying flaw (the "humans will adapt" bet *is* the control sacrifice).

**The classification:**
- Rendering / plumbing = **medium** → adopt the dominant industry choice (Three.js + react-three-fiber, quaternion math, bundler, file watcher). No hesitation; R3F is the correct non-weird pick.
- Control over the system (which DOF exist, that all stay directly controllable) = **substance** → design from need, ignore industry. Same category as "what a node is" / "how ticks work."

**The trap:** drei's `OrbitControls` is a *substance* decision (a control scheme) shipped *inside* a medium library (R3F/drei). That packaging is why it gets mistaken for medium and pattern-matched in. "Adopt R3F" (medium, yes) does NOT imply "adopt OrbitControls" (substance, no).

**Operative drift-guard — the recoverable-by-device test:** if a better input device does NOT restore a lost capability without changing the design, the loss is baked into the design → wrong industry pattern-match → reject. Click-tricks (dwell-to-summon pan pad, gesture timing) PASS — a 6-DOF device (SpaceMouse) restores the lost simultaneity, the loss was the device's. OrbitControls FAILS — a SpaceMouse still leaves a fixed pivot / locked roll, the loss is in the model.

Full design lives in the branch-local doc `docs/planning/visual-editor/3d-editor.md` (branch `task/editor-3d-plan`), which won't ride a merge — hence this memory. Related: [[feedback_code_self_defends]] (durable guard is structural code, not a doc/memory — encode the design-layer / device-fallback-layer split so implementing OrbitControls would violate a module boundary), [[feedback_substrate_vs_coordinator_bias]].
