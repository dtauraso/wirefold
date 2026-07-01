---
name: feedback_no_deferrals
description: When told "fix all 100%", fix every finding including low/off-hot-path ones — do not defer or offer defer-if-risky latitude
metadata:
  type: feedback
---

When David says "fix all of them 100%", every finding must be fixed — including
LOW-severity and off-hot-path items. Do NOT defer, and do NOT instruct subagents
to "defer if risky / leave a note instead." A finding's low severity is not a
license to skip it.

**Why:** David has repeatedly said "fix all 100%" across every cleanup pass, then
had to correct a deferral ("don't defer things") when a subagent left a LOW item
(the Trace.go per-position marshal) undone with just a comment.

**How to apply:** In subagent prompts, drop any "only if it's a clean/safe win,
else defer and report" escape clause. If an item seems risky (e.g. a perf change
that could alter output bytes), the instruction is to make it correctly and VERIFY
the concern is handled (e.g. assert byte-identical output), not to punt. If a
finding truly cannot be fixed, that is a STOP-and-ask, not a silent deferral.
Links: [[feedback_finish_calibrated_work]].
