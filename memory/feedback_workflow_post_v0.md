# Post-v0 workflow

Workflow rules that took effect after the visual-editor v0 closeout
(commit `913ccc3` on `task/smoothness-audit`). These override the
older "sign-off before every commit" rule that lived in CLAUDE.md
during v0.

This file mirrors the same rules into a Claude-Code-independent
location so any model picking up the repo can find them. CLAUDE.md
is the authoritative copy; this is the portable copy.

## Per-commit sign-off relaxed

- Commit and push freely on task branches.
- Sign-off still required for: merging task branches to `main`,
  force pushes, branch deletion, dependency removal, and
  destructive / shared-state actions.
- **Why:** signing off every commit was taxing for the user; editing
  or reverting committed code is cheap.
- **How to apply:** stop pausing for "ok to commit?" on task
  branches. Still build/run/verify before commit, and revert (don't
  leave broken state). Still pause before merging to main.

## Cost-marker rule: ≥$5 expected before recording

- Don't append `($N.NN)` markers to commits sized under ~$5
  expected.
- Bundle small commits into ≥$5 chunks for marker purposes.
- Pre-v0 sub-$5 markers stay as historical record; rule is
  going-forward only.
- **Why:** sub-$5 markers were noise relative to the variance.
- **How to apply:** size the chunk before starting; if expected
  <$5, bundle with adjacent work or skip the marker.

## Friction-driven, not phase-driven

- No new "Phase 10" docs. Plan-driven phase work is paused.
- New work is justified by friction logged in
  `../docs/planning/visual-editor/session-log.md` during real-world
  editor sessions.
- The NEXT UP candidates and per-phase follow-ups are now
  *candidates*, not commitments.
- **Why:** v0 shipped a lot of speculative work whose value is
  unproven; further phase planning would repeat the trap.
- **How to apply:** when the user surfaces friction, log it; don't
  propose multi-phase recovery plans. Small fixes on `task/fix-*`
  branches that merge to main fast.

## Audit work is friction-driven

- Audit categories: security, code smells, code quality, complexity,
  architectural tradeoffs, project-specific invariants (goroutine leaks,
  backpressure discipline, channel naming, spec/viewer hygiene),
  documentation drift, test quality, dependency freshness.
- **How to apply:** when user says "audit for X", load the relevant
  code and produce findings; don't fix in the audit pass.

## No AI-system lock-in

- Don't propose scheduled agents, Claude-Code skills/hooks/cron, or
  similar load-bearing infrastructure without explicit sign-off.
- Repo should stay portable across AI systems: plain markdown
  (CLAUDE.md, planning docs, this `memory/` directory,
  session-log.md) is enough for any model to be useful.
- **Why:** user wants AI dependency limited to "paying server costs."
- **How to apply:** audits, periodic checks, workflow extensions
  default to plain markdown + existing CI; bring up
  AI-system-specific options only when asked or when there's a
  clear win worth the lock-in.

## Working mode going forward

- User drives the editor and narrates observations.
- Assistant logs to session-log.md and makes changes.
- Debug sessions between user and assistant as needed.
