---
name: ease-of-fix is confounded evidence
description: Don't use "this fix was easy/hard" as standalone evidence that an architecture change is or isn't paying off; recency, surface area, and problem shape are usually present as confounds.
metadata:
  type: feedback
---

**Rule:** "This fix landed in one commit" is not standalone evidence that a
migration is paying off, and "this fix was painful" is not standalone evidence
that the old structure is in the way. Separate the factors before drawing an
architectural conclusion.

**Why:** The tempting inference is single-factor — *the fix was easy, therefore
the new locality is working*. But at least four things move together on any
recent migration, and they are hard to tell apart from the inside:

- **Locality.** The fix didn't have to reason about several pieces of global
  state at once.
- **Recency.** The rewritten code is days old, so it's fresh, and its contracts
  are already named in comments and tests.
- **Surface area.** New code is small; the thing it replaced was split across
  many files. Easier to hold in your head, independent of architecture.
- **Problem shape.** Some fixes are simple in *any* runtime. Those don't
  benchmark locality cleanly, so they can't be evidence for it.

Recency and surface area in particular are guaranteed to favor whatever was
built most recently — which is always the thing being evaluated. That is the
confound to distrust first.

**How to apply:**
- When the user reports ease or pain on a fix, don't convert it into a verdict
  on the architecture. Name the factors, and ask which one they mean.
- Be most suspicious when the evidence flatters a recent decision — that's when
  recency and surface area are doing the work unnoticed.
- This is a rule about *evidence*, not about clocks or transport. The original
  2026-05-07 instance (a pause/resume mid-arc fix in the retired pre-Go-clock
  runtime) is deliberately not detailed here: every symbol in it is dead, and
  the reasoning error survives its vocabulary. See
  [[feedback_dont_invent_doctrine]] for the adjacent failure — generalizing one
  incident into a rule.
