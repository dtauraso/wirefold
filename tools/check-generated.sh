#!/usr/bin/env bash
set -euo pipefail

# check-generated.sh — guard against stale generated files.
#
# Runs the generator and fails if any generated file differs from the committed version.
#
# THE FILE LIST IS DERIVED, NOT HARDCODED. The generator announces every file it writes
# ("gen-node-defs: wrote <abs path> ..."), so this parses that output and checks exactly
# what was written.
#
# It used to carry a hand-maintained list, duplicated in a header comment, and the two had
# already drifted: Buffer/node_kind_id_gen.go was in the checked list but missing from the
# comment. Worse than cosmetic — a NEW generated file added to the generator but not to the
# list was never guarded at all, silently and forever, because `git status --porcelain
# <explicit paths>` only looks where it is told. Deriving the list makes that
# unrepresentable: if the generator writes it, this checks it.
#
# Exit 0 when every generated file matches the committed version.

REPO_ROOT="$(git rev-parse --show-toplevel)" || {
  echo "check-generated: MISCONFIGURED — cannot locate repo root." >&2
  exit 1
}
cd "$REPO_ROOT"

GEN_OUT=$(cd tools/topology-vscode && npm run --silent gen:node-defs 2>&1) || {
  echo "check-generated: generator failed" >&2
  echo "$GEN_OUT" >&2
  exit 1
}

# "gen-node-defs: wrote /abs/path/file.ts (8 entries)" -> repo-relative path
FILES=$(printf '%s\n' "$GEN_OUT" \
  | sed -nE 's|^gen-node-defs: wrote ([^ ]+).*$|\1|p' \
  | sed "s|^$REPO_ROOT/||" \
  | sort -u)

# Refuse a vacuous pass: no parsed paths means the generator's output format changed and
# this guard is now checking NOTHING while reporting clean.
if [[ -z "$FILES" ]]; then
  echo "check-generated: MISCONFIGURED — parsed 0 generated files from the generator output." >&2
  echo "  Its 'wrote <path>' format likely changed; this guard would silently check nothing." >&2
  echo "  Generator said:" >&2
  printf '%s\n' "$GEN_OUT" | sed 's/^/    /' >&2
  exit 1
fi

# Every announced file must be tracked — an untracked generated file is invisible to
# `git status --porcelain <path>` diffing and would pass vacuously.
UNTRACKED=0
while IFS= read -r f; do
  [[ -z "$f" ]] && continue
  if ! git ls-files --error-unmatch "$f" >/dev/null 2>&1; then
    echo "check-generated: generated file is not tracked by git: $f"
    echo "  (an untracked generated file can never be reported stale — commit it)"
    UNTRACKED=1
  fi
done <<< "$FILES"
[[ $UNTRACKED -eq 0 ]] || exit 1

# shellcheck disable=SC2086 # word-splitting is intended: FILES is a newline list of paths
stale=$(git status --porcelain -- $FILES 2>/dev/null || true)

if [ -n "$stale" ]; then
  echo "check-generated: stale generated file(s) — commit the regenerated output:"
  echo "$stale"
  exit 1
fi

echo "check-generated: clean ($(printf '%s\n' "$FILES" | grep -c .) generated files checked)"
exit 0
