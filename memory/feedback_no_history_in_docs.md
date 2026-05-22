---
name: no-history-in-docs
description: Planning and tracking docs should describe forward plan only; status markers, commit hashes, and completion notes go to git log
metadata:
  type: feedback
---

# Don't Put History in Planning Docs

**Rule:** planning/tracking/staging docs must contain only forward-looking plan — no "Done"/"Not started" status, no commit hashes, no "completed on" markers, no historical narration.

**Why:** git log is the source of truth for history; mirroring status into docs creates a second drift surface that must be hand-maintained and goes stale silently. User flagged this on 2026-05-22 after a stages-tracking doc had been written with "Status: Done" + commit hash.

**How to apply:** when writing or updating docs in `docs/planning/`, `memory/MEMORY.md` entries, or any non-`session-log.md` markdown — describe what the stage/task *is*, not where it stands. If a reader needs to know what's done, point them at `git log`. Exceptions: `session-log.md` and `handoff.md` are explicitly history-bearing per CLAUDE.md.
