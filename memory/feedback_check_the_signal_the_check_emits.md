---
name: check the signal the check actually emits
description: Self-derived (not David's). Before reporting "verified", confirm the check's success signal can express failure — make it fail once. Recurred 6x in one session.
metadata:
  type: feedback
---

**Provenance:** self-derived from my own repeated mistakes on 2026-07-15, NOT
guidance David gave. Recorded because the failure recurred six times in one
session, not because it was asked for. Weigh it accordingly — and see
[[feedback_dont_invent_doctrine]] before promoting any of it further.

**Rule:** Before reporting something as verified, ask what signal the check emits
**and whether that signal can express the failure you care about**. Running a real
check and reading the wrong channel is worse than not checking: it produces a
confident, false "verified."

**Why this shape recurs:** the check is real, it runs, and it returns its success
value honestly. The failure is reading a channel that is constant, or scoped to
the wrong object, then treating its silence as evidence. A check that cannot fail
reads exactly like a passing one.

**The six, all one session, all "checked the object next to the one that mattered":**
`$?` instead of stdout (see the verify recipe in CLAUDE.md — raw `stop-checks.sh`
always exits 0 and speaks the hook's JSON protocol; the terminal fix is now
`scripts/verify.sh`, a `--cli` wrapper that exits NONZERO on failure, so at the
terminal `$?` is finally the right channel — but only for verify.sh, never raw
stop-checks.sh);
`git branch -d`'s unmerged-*commit* check on a branch whose entire content was its
`branch.<name>.description` (git has no safety check for descriptions — they are
local config and deletion is unrecoverable); `git status` on the file I edited
rather than on what a regeneration then rewrote; guard *wrappers* instead of the
audit scripts they wrap; `.claude/hooks/` instead of `tools/` for a hook that
existed all along. And twice: relaying a subagent's claim as fact without probing
it — one of which (`settings.local.json` allowlisting `git merge`) was simply
invented, and I repeated it to David in two summaries.

**How to apply:**
- **Make it fail on purpose once.** If you cannot produce a failing run, you do
  not know what failure looks like and cannot claim success. Every instance above
  was settled by one deliberate violation.
- Name the channel when reporting: "empty stdout from stop-checks", not
  "verified". Vague verbs hide which signal was read.
- Prefer the signal the *tooling* consumes — the Stop hook reads stdout, so read
  stdout.
- A subagent's finding is a hypothesis, not a fact. Probe before relaying. In this
  session roughly half of them were wrong, including the top-ranked one.

See [[feedback_headless_repro_verifies_persistence]] (green signals that never
exercised what they claimed) and [[feedback_verify_subagent_commits]].
