---
name: check the signal the check actually emits
description: Before reporting "verified", confirm the check's success signal can express failure. stop-checks always exits 0 (failure goes to stdout as hook JSON); git branch -d only sees commits, not the description.
metadata:
  type: feedback
---

**Rule:** Before reporting something as verified, ask what signal the check
emits **and whether that signal is capable of expressing the failure you care
about**. Running a real check and reading the wrong channel is worse than not
checking: it produces a confident, false "verified."

**Two instances, same shape, one session (2026-07-15):**

- **`scripts/stop-checks.sh` ALWAYS exits 0.** It speaks the Stop-hook JSON
  protocol: on failure it prints `{"decision":"block","reason":...}` to **stdout**
  and still exits 0. So `stop-checks.sh >/dev/null 2>&1; echo $?` reads a
  constant and discards the only failure signal. I reported "stop-checks exit: 0"
  four times on a branch the Stop hook then blocked. **Clean means empty stdout.**
  Gate on `[ -z "$(bash scripts/stop-checks.sh 2>/dev/null)" ]`.

- **`git branch -d` only checks commits.** It refuses to delete unmerged
  *commits*. A `task/*` branch whose entire content is its
  `branch.<name>.description` (per CLAUDE.md, descriptions ARE the open-work
  record) has nothing for `-d` to protect. Deleting one and reporting "confirming
  nothing was lost" is exactly backwards: the check structurally could not see the
  thing at risk. Descriptions are local git config — deletion is unrecoverable and
  invisible to every git safety check.

**Why this shape recurs:** both checks are real, both ran, both returned their
success value honestly. The failure is choosing a channel that is constant
(`$?` here) or scoped to the wrong object (commits, not descriptions), then
treating its silence as evidence. A check that cannot fail is not a check, and it
reads exactly like a passing one.

**How to apply:**
- Before trusting a check, make it fail on purpose once. If you cannot produce a
  failing run, you do not know what failure looks like and cannot claim success.
  (Both instances above were settled in one probe each.)
- Name the channel when reporting: "empty stdout from stop-checks", not
  "verified". Vague verbs hide which signal was read.
- Prefer the signal the *tooling* consumes. The Stop hook reads stdout; so should
  you.

See [[feedback_headless_repro_verifies_persistence]] — same family: green
signals that never exercised the thing they claimed to cover.
