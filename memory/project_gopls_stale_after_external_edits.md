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

**Confirmed root cause (researched 2026-07-19, investigation CLOSED — do not
re-run it):** gopls does not poll the filesystem. It relies entirely on the LSP
client sending `workspace/didChangeWatchedFiles`, and for files changed outside
the editor it does not rebuild cached AST/type-check data unless notified. The
documented symptom in golang/go#31553 is exactly ours: a `git checkout` — which
adds, deletes and rewrites files — leaves gopls reporting results for file
content that no longer exists. Agent edits via Bash/`perl`/`sed` plus branch
checkouts are that case continuously.

**There is NO setting that fixes this.**
- golang/go#31553 — open since April 2019, `NeedsFix`. Root gap, unresolved.
- golang/go#67995 — server-side file watching (gopls watching the FS itself,
  via fsnotify) is an UNIMPLEMENTED proposal on the backlog. gopls filed it
  because client file watching is "spotty and inconsistent" across editors.
- golang/go#40812 — a work-around for a VS Code file-watching bug specifically.
- VS Code uses parcel-watcher with event correlation deliberately DISABLED for
  stability; `files.watcherExclude` can swallow events, but that is a separate
  cause and is NOT set in this repo (checked — workspace and user settings both
  clean), so it is not what bites here.

Editor-side remedy is the only one: `Go: Restart Language Server` (cheap) or
`Developer: Reload Window` (see [[feedback_two_process_editor_reload]] for why
reopening a file is not enough). Expect to need it after any bulk external edit.

Related: [[feedback_guards_hardcoding_single_file_break_on_split]] — file splits
break things that hold a stale picture of where code lives; this is the same
class, one layer down in the tooling.
