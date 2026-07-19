#!/usr/bin/env bash
set -euo pipefail

# check-no-untracked-source.sh — fail when a SOURCE file exists on disk but is
# not tracked by git, because six guards in this suite are blind to such files.
#
# The problem: these guards build their corpus from `git ls-files`, which lists
# TRACKED files only —
#
#   check-doc-symbols.sh        (symbol corpus + SPEC/doc scan)
#   check-doc-citations.sh      (cited-path resolution)
#   check-no-nul-bytes.sh       (literal 0x00 scan)
#   check-send-rule-parity.sh   (nodes/Wiring/*.go send-rule scan)
#   check-generated.sh          (asserts each generated file is tracked)
#   check-kind-imports.sh       (asserts the generated import file is tracked)
#
# so a brand-new file gets ZERO coverage from any of them until it is tracked.
# That is backwards: new code is the most likely to have a problem and the
# least likely to be seen.
#
# It fails in both directions, and the silent one is worse:
#   - LOUD:   a symbol moved into a new untracked file looks like a ghost to
#             check-doc-symbols, so it reports a false positive. Annoying, but
#             at least you find out. This happened twice on 2026-07-19 while
#             splitting files.
#   - SILENT: a new untracked file containing a NUL byte, a send-rule
#             violation, or a doc citation to nowhere passes every guard
#             cleanly, because none of them can see it. Nothing tells you.
#
# Why this guard rather than auto-staging: a check script must not quietly
# mutate the index. Failing loudly with the exact command to run keeps the
# decision with the author and makes the blindspot impossible to sit in.
#
# The fix it prescribes is `git add -N` (intent-to-add): the path becomes
# visible to `git ls-files` — and therefore to every guard above — while the
# content stays unstaged, so it does not disturb a partial staging workflow.
#
# Exit 0 when clean, exit 1 (naming each file) when untracked source exists.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

# Source extensions the git-ls-files-driven guards actually care about. Kept in
# sync with the union of their own include patterns (see the header list).
INCLUDE_RE='\.(go|ts|tsx|js|jsx|sh|py)$'

# --others = untracked; --exclude-standard honours .gitignore, so build output,
# node_modules, and scratch files never enter the scan. Intent-to-add paths are
# already tracked, so they correctly do NOT appear here.
#
# Read with a while-loop rather than `mapfile`: macOS ships bash 3.2, which has
# no mapfile, and this suite runs there. An unguarded mapfile exits 127 — a
# failure mode that reads like a broken script, not like a caught problem.
untracked=()
while IFS= read -r f; do
  [ -n "$f" ] && untracked+=("$f")
done < <(git ls-files --others --exclude-standard -z \
  | tr '\0' '\n' \
  | grep -E "$INCLUDE_RE" || true)

if [ ${#untracked[@]} -eq 0 ]; then
  echo "no-untracked-source: clean (every source file is visible to git ls-files)"
  exit 0
fi

echo "no-untracked-source: ${#untracked[@]} untracked source file(s) — these are INVISIBLE to the six git-ls-files-driven guards (doc-symbols, doc-citations, no-nul-bytes, send-rule-parity, generated, kind-imports), so they are currently unchecked:" >&2
for f in "${untracked[@]}"; do
  echo "  $f" >&2
done
echo >&2
echo "Make them visible without staging their content:" >&2
printf '  git add -N' >&2
for f in "${untracked[@]}"; do
  printf ' %q' "$f" >&2
done
echo >&2
exit 1
