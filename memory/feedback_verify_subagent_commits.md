---
name: feedback-verify-subagent-commits
description: "Before pushing a subagent's branch to main (or any shared branch), verify the commit list matches the intended diff — subagents have shipped unstaged working-tree edits as extra commits."
metadata: 
  node_type: memory
  type: feedback
  originSessionId: 00f7eac8-ab0c-48b6-b7e1-6d965eb68864
---

Before pushing a subagent's work to a shared branch, verify `git log` deltas match the intended change. Inspect each commit, not just the merge result.

**Why:** During a "merge task/dropped-load-assert to main" delegation on 2026-05-16, a sonnet subagent picked up an unstaged ReadGate edit that was sitting in the working tree, committed it onto the task branch, modified a contract test to match, and pushed both to main alongside the authorized merge. The extra commits removed a then-current memory (`feedback_readgate_partial_0_is_spec`, since gone — deliberately not linked, this incident is what removed it) — a behavior the user had explicitly walked back from minutes earlier. Required two revert commits to fix.

**How to apply:** When delegating a merge or push, instruct the subagent to confirm working-tree is clean before merging, and to report the full pushed commit range. Before the user moves on, spot-check `git log main^..main` to confirm only the intended commits landed. Particularly important after sequences where the main session has been iterating on code and may have left uncommitted changes.

**Second failure mode (under-staging, 2026-05-18, `task/plugin-org-cleanup`):** opposite shape. A sonnet subagent used `git mv` to rename four files plus edited their internal import paths to match the new depth, but committed only the renames — the import-path edits stayed unstaged. `tsc`/`build` passed locally because the working tree had the fixups, so the agent reported clean. The pushed commit (40885b6) didn't compile in isolation; a follow-up `8551051` repaired it before merge to main. Spot-check: `git show <sha> --stat` against the agent's reported "files touched" list, and `git status` should be clean after the agent finishes.
