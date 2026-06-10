---
name: feedback-dont-invent-doctrine
description: Do not paraphrase project notes into "rules" and then cite the paraphrase as doctrine; if a constraint matters, grep for the literal source and quote it
metadata:
  type: feedback
---

When invoking a project rule to constrain a design choice, the rule must exist as written. Paraphrasing a one-off framing in a handoff doc into a general principle, and then citing the paraphrase as if it were doctrine, is a way to manufacture objections out of thin air. Same failure shape as [[feedback-no-manufactured-shortcuts]] and [[feedback-invariants-drive-design]].

**Why:** During pulse-transport Phase 6 design, I objected to Go computing Bezier arc length on the grounds that "Go is a pure function of spec; layout is render-policy" — a phrase I claimed was project doctrine. The user grepped: it appears nowhere in the codebase. I had loosely paraphrased "Layout geometry intentionally feeds Go timing" (a specific note about this refactor) into a general purity principle, then bounced architectural decisions off my own paraphrase. The user: "I'm starting to think the context 'understanding' AI does is a big lie. 'Pure function' in that context was not meant to be treated as a pure coding design." Right. I inflated a soft framing into a rule and used it to argue against the model-correct path.

**How to apply:**
- Before invoking a project rule, grep for the literal phrasing. If it doesn't exist verbatim, don't quote it as doctrine.
- Distinguish between a *specific note about a specific decision* and a *general principle*. The former does not generalize without permission.
- If a constraint feels load-bearing, surface it as "I have a concern about X — is that an actual rule here, or my own framing?" rather than asserting it.
- Doctrine-citation is a form of manufactured objection. Catch it the same way: ask whether the objection survives without the cited rule. If yes, lead with the real reason. If no, drop the objection.

Related: [[feedback-no-manufactured-shortcuts]], [[feedback-invariants-drive-design]], [[feedback-derive-model-from-visual-spec]].
