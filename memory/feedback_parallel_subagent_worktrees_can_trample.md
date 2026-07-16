---
name: feedback_parallel_subagent_worktrees_can_trample
description: Parallel implementer subagents may run in isolated git worktrees and copy edits back into the main tree, trampling a concurrently-active branch
metadata:
  type: feedback
---

When several `implementer` subagents are launched in parallel to edit disjoint
file sets, some of them run in **isolated git worktrees** (based off the commit
the parent was on, not the live branch) and then **copy their edited files back
into the main working tree** at the end. Two failure modes hit at once during the
2026-06-29 code-smell audit:

1. A worktree based off `main` copied its `NavGuides.tsx` over the main tree's
   copy, which was on `task/ui-polar-locks` with committed octant work →
   silently reverted 21 octant references in the working tree (recoverable only
   because they were committed).
2. One subagent ran `git checkout HEAD -- <dir>` in the main tree thinking it was
   cleaning artifacts, discarding sibling subagents' copied-back edits.

**Why:** the main checkout can also be switched out from under the run (a
concurrent session committed to `ui-polar-locks` and switched the shared working
dir), so "the main tree" is not a stable base while parallel writers and worktree
copy-backs are in flight.

**How to apply:** for parallel edit fan-out, (a) verify `git branch --show-current`
before and after; (b) treat each subagent's **worktree** as the canonical source
(it's cleanly based off the branch point) rather than the main tree; (c) capture
each area's diff as a patch / copy from the worktree, reset the main tree to clean
HEAD to protect any concurrently-active branch, then re-apply onto the intended
branch; (d) snapshots taken mid-flight can predate a subagent's final STOP
decisions — reconcile commit messages against the actually-committed code, not the
agent's report. Builds/tests passing is the ground-truth check that the
reconstructed state is internally consistent. Related: [[feedback_verify_subagent_commits]],
[[feedback_no_nested_agents]].

**2026-07-04 recurrence — trampling happens even WITHOUT worktrees.** Ran 6
parallel `implementer`s over disjoint file sets with an explicit "do NOT create a
worktree, edit directly" instruction. Still corrupted the shared tree because one
agent ran `git stash`/`git stash pop` (a whole-tree op) to isolate its own build
check — that stashed the other 5 agents' in-flight edits into `stash@{0}`, and the
pop hit conflicts and left work stranded. Symptoms: items reported "done" but
reverted to HEAD on disk; a half-applied refactor with orphan/unused helpers; a
stray `stash@{0}` holding the trampled edits. Recovery: stop remaining agents to
quiesce, then `git checkout stash@{0} -- <only the files that were stranded>` to
surgically extract (leaving newer verified versions in the tree), reset half-applied
files to HEAD, bisect any behavioral regression by reverting refactor files in
groups, then commit. **Lessons:** (1) the file-disjoint split is NOT sufficient —
ANY git tree/index mutation by a subagent (`stash`, `checkout`, `reset`, `add`) is
global and tramples siblings; the delegation prompt must forbid all mutating git,
not just worktrees. (2) A refactor's blast radius can exceed its named file set
(splitting a struct broke call sites in `node_move.go` + test files outside the
agent's scope — the specific symbol, `fadePersister`, has since been removed with
the fade feature; the blast-radius lesson is what matters) — scope the agent to
the call sites too, or serialize.
(3) When trampling is likely (shared tree, overlapping-ish concerns), prefer
SEQUENTIAL implementers or worktree isolation over disjoint-file parallelism.
