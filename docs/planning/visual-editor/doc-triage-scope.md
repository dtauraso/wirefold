# Planning-Doc Triage — Scope and Approach

**Status:** scratch  
**Created:** 2026-05-21  
**Branch:** task/planning-doc-triage

---

## Problem

Every subagent spawn re-reads CLAUDE.md, MEMORY.md, and any docs named
in the prompt. The planning-doc surface in `docs/planning/visual-editor/`
is the expensive part: live, scratch, and superseded content are
intermingled, so subagents either read everything or guess which files
matter. The tax is per-spawn and compounds across haiku/sonnet subagents.

`memory/` is already indexed and one-line-per-entry — that shape is right
and is not the problem. The expensive surface is the planning-doc directories.

---

## Goal

**Shrink the planning-doc surface so subagent spawns read less.**

End state:
- Every file at HEAD is current. To see how something used to look, use
  `git log <path>` or `git log --follow`. That's what version control is for.
- Fewer files total under `docs/planning/visual-editor/`.
- Each remaining file is unambiguously authoritative on exactly one topic.
- Scratch notes are either folded into the active handoff or deleted — not
  left as free-floating files.
- `archive/` is removed as part of this work. Its contents are already in
  git history; `git log` is the archive.

Annotation is a means to reach this end, not the end itself. Tagging
40 files `archived` does not reduce read cost if they remain in the live
directory and show up in greps. The tag is a triage worksheet marker,
not the deliverable.

---

## Why Annotation Is Transitional, Not Terminal

The prior approach proposed adding `status:` frontmatter as the primary
output. That is the wrong shape:

- A tagged-but-present file still appears in directory listings and greps
  unless the grep caller excludes it by name.
- Adding frontmatter to 40 files is a lot of commits for zero reduction
  in file count.
- The real cost driver is surface area (file count × file size), not
  legibility of status.

Annotation is useful as a **worklist step only**: walk the files, mark each
with a provisional disposition, then act immediately. The annotation is a
"delete in next commit" marker — it never lands at HEAD. Once the surface
is reduced, no frontmatter tagging convention is needed.

---

## Process

### Step 1 — Inventory

Walk every file under `docs/planning/` and root-level `*.md` (excluding
`CLAUDE.md`, `MODEL.md`, `README.md` which are not planning docs).

For each file, assign one of three dispositions:

| Disposition | Action |
|-------------|--------|
| KEEP | Authoritative; stays in place |
| MERGE | Scratch worth folding into a KEEP doc; source file then deleted |
| DELETE | Superseded or redundant; remove via `git rm` |

There is no MOVE disposition. Git history is the archive. Superseded docs
are deleted; history remains accessible via `git log <path>`.

Surface any ambiguous cases (partially superseded, scope overlaps with
another live doc) for user review before acting.

### Step 2 — Apply in batched commits

- MERGE targets: fold content into the authoritative destination, then
  `git rm` the source file.
- DELETE targets: `git rm`.
- KEEP targets: no change in this pass; they are the survivors.
- `archive/` directory: `git rm -r` — its contents are already in git history.

### Step 3 (optional) — Add `status:` frontmatter to survivors

If the survivor set is small enough that a human can review it, adding
`status: authoritative` to each remaining file is a useful forcing function:
declaring authority is harder than tagging, and the act surfaces any
remaining ambiguity. Drop the tag convention once the surface is stable
enough that the index speaks for itself.

---

## Open Questions

1. **Are there any docs that future sessions genuinely cannot reconstruct
   from `git log` + current code?** If so, those are the keepers. Everything
   else is a delete-candidate. The reconstruction test is the right filter —
   not "does it still look useful at a glance."

2. **Are there docs that look authoritative but are superseded?**  
   Some docs may describe a design replaced by a later session-log entry or
   more recent planning doc. Heuristic: if no handoff.md or MEMORY.md entry
   references the file within the last N commits, it is a deletion candidate.

3. **MERGE vs. DELETE authority for scratch files.**  
   Does every decision need user sign-off per file, or is a rule like
   "not referenced from handoff.md or MEMORY.md within the last 5 commits
   → delete" workable as a default with opt-out? The answer determines
   whether the triage pass is one agent session or an interactive review.

---

## Non-Goals

- This does not change how surviving docs are written or what they contain.
- This does not touch `memory/` — the index is already the right shape.
- This does not add new planning docs; scope is reduction only.
- This does not change CLAUDE.md.
