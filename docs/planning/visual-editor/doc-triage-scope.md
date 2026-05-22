# Planning-Doc Triage — Scope and Approach

**Status:** scratch  
**Created:** 2026-05-21  
**Branch:** task/planning-doc-triage

---

## Problem

### Surface size

The planning-doc surface in this repo is large and spans multiple locations:

- `CLAUDE.md` — project instructions loaded on every agent spawn
- `MODEL.md` — substrate model and banned vocabulary
- `memory/MEMORY.md` — memory index (loaded by harness as system-reminder)
- `memory/*.md` — individual memory files (40+ entries)
- `docs/planning/visual-editor/` — active and archived planning docs
- Root-level `*.md` — README, handoff schema docs, etc.

Every subagent spawn pays a re-read cost proportional to this surface.
The cost is not just tokens — it is latency and judgment load on a model
that may be cheaper and less capable than the main session.

### Distinguishing authoritative from stale is manual

Agents cannot tell at a glance which docs are:

- **Authoritative** — the current source of truth that must be applied
- **Scratch** — active in-progress thinking that is not yet settled
- **Archived** — superseded; kept for history but not prescriptive
- **Index** — a map to other docs; read it to decide what else to read

Without a signal, agents either read everything (expensive) or guess
(error-prone). The current heuristic is directory path and filename
conventions, which are inconsistently applied and not machine-readable.

### Greps surface archive content alongside live content

`grep` and `find` commands over `docs/planning/visual-editor/` return hits
from the `archive/` subdirectory alongside live docs. Agents then spend
reads judging whether a hit is live or historical. The CLAUDE.md bash-hygiene
rule already calls out `handoff-archive` in the exclude list, but the pattern
is not generalized and requires every caller to remember it.

### Subagent cold-start compounds the problem

Each spawned subagent (haiku for grep work, sonnet for mechanical edits)
re-reads the memory index and any docs the main session told it to read.
If those docs contain archived or scratch content mixed with authoritative
content, the subagent re-derives staleness judgments from prose — exactly
the judgment work that should have been done once and recorded structurally.

---

## Proposed Approach

The following is a sketch only. No implementation yet.

### 1. Status tag on every planning doc

Tag each doc with one of four statuses:

| Tag | Meaning |
|-----|---------|
| `authoritative` | Current source of truth; agents must apply it |
| `scratch` | Active in-progress thinking; may be incomplete or contradictory |
| `archived` | Superseded; kept for history; agents should not apply it |
| `index` | Links to other docs; read it to decide what else to open |

### 2. Tag lives in YAML frontmatter

Place the tag at the very top of each file so it is visible without
scrolling and parseable without prose judgment:

```yaml
---
status: authoritative   # authoritative | scratch | archived | index
---
```

Agents reading the first few lines of a file can determine whether to
continue reading or skip. This is cheaper than reading the full file and
deriving staleness from content.

### 3. Memory index and new index docs use inline status

`memory/MEMORY.md` already lists entries with one-line descriptions.
Extend each entry to include the status inline:

```
- [feedback_foo.md](feedback_foo.md) `authoritative` — Description of the rule
- [project_bar.md](project_bar.md) `archived` — Superseded by baz pattern
```

Agents reading the index can then skip archived entries without opening
the files they reference. This eliminates the most common cold-start waste:
reading a memory file only to discover it describes a pattern that was
reverted.

### 4. A short repo-root convention doc

Create one doc (e.g. `docs/planning/doc-status-convention.md`) that:

- Defines the four tags and their agent-facing semantics
- States where frontmatter goes and what the exact YAML key is
- Lists which directories are covered by the convention
- Notes that `archived` docs should not be applied even if stumbled upon

This doc is itself tagged `authoritative` and is short enough to load
cheaply on spawn.

### 5. Audit pass

Walk every file under:

- `docs/planning/`
- `memory/`
- Root-level `*.md`

For each file, propose a tag assignment. Surface any ambiguous cases
(docs that are partially superseded, docs whose scope overlaps an
active doc) for user review before committing tags.

The audit is the only step that touches existing files. Steps 1–4 are
additive.

---

## Open Questions

1. **Archive subdirectory vs. in-place tag?**  
   Some docs already live in `docs/planning/visual-editor/archive/`.
   Should archived docs move into a consistent `archive/` subdirectory
   at each level, or stay in place with the tag? Moving makes grep
   exclusion easy (`--exclude-dir=archive`); staying in place avoids
   git-blame disruption and broken links.

2. **Machine-readable (CI lint) or doc-only?**  
   A CI script could reject PRs that add a doc without a `status:`
   frontmatter key. This prevents tag drift but adds tooling overhead.
   Alternative: lint is advisory only and runs as an audit (kind 1 in
   the audit registry) rather than a gate.

3. **Does CLAUDE.md need a pointer to the convention?**  
   CLAUDE.md is loaded on every agent spawn. If the convention doc is
   not referenced there, agents may not know to consult it when
   encountering an untagged file. A single line — e.g. "Doc status
   tags: see `docs/planning/doc-status-convention.md`" — would close
   this gap without meaningfully expanding spawn cost.

4. **Scope of the initial audit pass?**  
   `docs/planning/visual-editor/` has both live docs and an `archive/`
   subdirectory. The memory dir has 40+ files. Root-level `*.md` includes
   `CLAUDE.md`, `MODEL.md`, and `README.md` which are not planning docs
   but are part of the agent-facing surface. Decide whether the
   convention applies to those or only to planning and memory files.

5. **Tag assignment authority?**  
   Should the initial tag assignments be proposed by an agent and
   confirmed by the user, or should the user assign tags directly?
   Agent proposal is faster but risks misclassifying docs the agent
   has not fully read. User confirmation is the gate either way;
   the question is whether the proposal step is worth the token cost.

---

## Non-Goals

- This does not change how docs are written or what they contain.
- This does not merge or delete any existing doc; that is a separate
  decision per doc.
- This does not change CLAUDE.md content beyond a possible one-line
  pointer (open question 3 above).
- This does not add new planning docs for features; scope is metadata
  and navigation only.
