#!/usr/bin/env bash
set -euo pipefail

# nudge-file-watcher.sh — re-emit a filesystem event for every source file this
# session changed, so the editor's file watcher gets a second chance to notice.
#
# WHY THIS EXISTS
#
# gopls does not poll the filesystem. It relies entirely on the LSP client
# (VS Code) sending `workspace/didChangeWatchedFiles`, and for files changed
# OUTSIDE the editor it does not rebuild its cached AST/type-check data unless
# it is told to (golang/go#31553, open since 2019, still NeedsFix). Agent edits
# via Bash/perl/sed, plus branch checkouts during a merge, are exactly that case
# — continuously.
#
# When the watcher misses an event, gopls keeps the OLD copy of the file. If the
# file SHRANK (a split, an extraction), gopls then holds both the old full copy
# and the new file the content moved into, so every moved symbol reads as
# doubly declared. Observed symptoms, all phantom: "X redeclared in this block",
# "undefined: X", "too many arguments in call to X" — with line numbers that do
# not exist in the file on disk.
#
# THE THEORY THIS SCRIPT ACTS ON
#
# VS Code watches via parcel-watcher, with event correlation deliberately
# disabled for stability. Bulk rapid changes are the case most likely to drop or
# coalesce events. A single `touch` per file, once the filesystem is quiet, is a
# clean isolated event that the watcher is much more likely to deliver.
#
# This is a MITIGATION, not a fix. The real fix is server-side file watching in
# gopls (golang/go#67995), which is an unimplemented backlog proposal. If
# phantom diagnostics persist after this runs, the remedy is unchanged:
# `Go: Restart Language Server` from the command palette.
#
# WHY NOT `pkill gopls`: the extension may decline to restart it — "Connection
# to server got closed. Server will not be restarted." That trades a stale
# language server for no language server.
#
# SAFETY: touch alters mtime only, never content. Go's build cache is content-
# hashed, so builds and tests are unaffected.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

INCLUDE_RE='\.(go|ts|tsx)$'

# Changed vs HEAD (staged + unstaged) plus untracked: the full set an agent may
# have written this session. --exclude-standard keeps ignored build output out.
{
  git diff --name-only HEAD -- . 2>/dev/null || true
  git ls-files --others --exclude-standard 2>/dev/null || true
} | grep -E "$INCLUDE_RE" | sort -u > /tmp/.nudge-list.$$ || true

count=0
while IFS= read -r f; do
  [ -f "$f" ] || continue           # skip deletions
  touch "$f"
  count=$((count + 1))
done < /tmp/.nudge-list.$$
rm -f /tmp/.nudge-list.$$

if [ "$count" -gt 0 ]; then
  echo "nudge-file-watcher: re-emitted events for $count changed source file(s)"
fi
exit 0
