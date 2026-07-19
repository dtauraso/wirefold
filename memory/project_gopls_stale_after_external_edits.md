---
name: project-gopls-stale-after-external-edits
description: Editor diagnostics go stale after external edits (esp. file splits) — gopls keeps the OLD copy of shrunken files; `go build` is authoritative
metadata:
  type: project
---

The `<new-diagnostics>` blocks in this harness can report compile errors that
are **not real**. Diagnosed empirically 2026-07-19.

**Mechanism:** gopls serves a stale copy of files modified OUTSIDE its write
path (subagent edits via Bash/`sed`/`perl`, and file splits generally). It picks
up NEWLY CREATED files immediately and correctly — verified by planting a real
duplicate in a fresh untracked file, which it flagged instantly and accurately.
What it misses is a file that SHRANK. It then holds both the old full copy and
the new file the content moved into, so every moved symbol looks doubly
declared.

**Proof:** after splitting `builders.go` 719 → 491 lines, diagnostics cited
`builders.go:643` and `:664` — line numbers that do not exist in the file.
`go build` reported nothing.

**Symptoms, all bogus:** `X redeclared in this block`, `undefined: X`,
`md.<field> undefined`, `too many arguments in call to X`. Concentrated right
after a file split or a struct-field rename.

**How to apply:** `go build ./...` (and `go vet`) are authoritative — ALWAYS
check before acting on a diagnostic that appears after an edit sequence. Do not
"fix" a phantom error; you will damage working code. This cost repeated
verification round-trips in one session before being diagnosed.

Not fixable from the agent side — it is the language server's cache. Editor-side
fix is `Go: Restart Language Server` (cheaper) or `Developer: Reload Window`
(see [[feedback_two_process_editor_reload]] for why reopening a file is not
enough).

Related: [[feedback_guards_hardcoding_single_file_break_on_split]] — file splits
break things that hold a stale picture of where code lives; this is the same
class, one layer down in the tooling.
