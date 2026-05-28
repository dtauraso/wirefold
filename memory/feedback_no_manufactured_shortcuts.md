---
name: feedback-no-manufactured-shortcuts
description: Don't offer partial/local-patch variants as decision options when the model already prescribes the full path; subagent typing cost is not a real tradeoff axis
metadata:
  type: feedback
---

When the substrate model already prescribes how a change should land, do not present "smaller / TS-local / defer" variants alongside the model-correct path as if they were peer options. Picking the model-correct path is not "more work" — it is the work.

**Why:** During pulse-substrate-transport Phase 4, I presented three options for latency-live on drag: (A) TS-local recompute, (B) TS→Go node-move, (C) defer. The model already says substrate owns transport timing — B was the only path consistent with the model; A reintroduced duplicated formulas (the exact drift Phase 3 just removed), and C left the feature half-built. The user pushed back: "why are partial work items being promoted as the first option? I'm more confused about a fast typist suggesting it does less typing." Subagent execution cost is not a constraint worth trading model coherence against.

**How to apply:** Before listing options, check whether the model/contract already decides the question. If yes, dispatch the model-correct path and only ask the user about genuinely open sub-decisions (units, naming, scope edges). If you catch yourself listing a "smaller" option whose only virtue is "less code to write" or "fewer files touched," delete it — that's manufactured. Real tradeoffs involve correctness, blast radius, user-facing behavior, or future-flexibility — not typist effort.

Related: [[feedback-substrate-vs-coordinator-bias]] (knob-tuning is the wrong shape; find the missing local signal), [[feedback-derive-model-from-visual-spec]] (derive the implied model up front; refuse cheap patches that preserve a wrong model).
