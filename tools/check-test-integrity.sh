#!/usr/bin/env bash
set -euo pipefail

# check-test-integrity.sh — makes tests-edited-to-go-green VISIBLE and DELIBERATE.
#
# The verify suite (go test, vitest, staticcheck, eslint, 30+ guards) all asks the same
# question: "does the suite pass?" None asks "did the suite change IN ORDER to pass?" An
# agent that deletes an assertion, loosens a comparison, adds t.Skip / .only, or drops a
# table case produces the same green as one that fixed the bug — and the Stop hook accepts
# it. This is the last big false-green escape route in the repo; every other rule here is
# guarded, and the verify gate itself was the exception.
#
# DETECTION, NOT PROHIBITION. Tests must stay editable — new tests, renames, refactors, and
# genuinely-wrong assertions being corrected are all legitimate. This guard fails only when a
# test change SHEDS strength (a fix should generally add or hold assertions, not lose them),
# and it names the file and what was lost so the change is a deliberate decision, not a
# reflex.
#
# Signals (diffed against the merge-base with origin/main, matching stop-checks' branch-ahead
# gating — so it is a no-op on main and only looks at this branch's own changes, committed or
# working-tree):
#   1. NET assertion loss across all changed test files (t.Fatal/Error[f], require./assert.,
#      expect(). Counted across ALL files so moving a test between files is net-zero.
#   2. Newly ADDED skips/onlys: t.Skip(Now), .skip(, .only( (worst — silently disables every
#      OTHER test in a vitest file), xit/xdescribe.
#   3. Newly ADDED os.Exit / recover() inside test files (fake-pass / swallow-failure tricks).
#
# ESCAPE HATCH WITH A COST: put the marker  [allow-test-weakening]  in a commit message on the
# branch to state a deliberate removal (mirrors how strip-branch-local-docs keys off a
# marker). It is a commit-message marker ON PURPOSE — not a CLI flag the agent can pass itself,
# and not silent. Uncommitted weakening stays flagged until you commit it with the marker.
#
# Exit 0 clean, exit 1 with a named report.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

# Base ref, same precedence stop-checks.sh uses. No base (no origin/main and no main, e.g. a
# shallow CI checkout) → nothing to diff against, no-op.
if git rev-parse --verify -q origin/main >/dev/null 2>&1; then
  base_ref="origin/main"
elif git rev-parse --verify -q main >/dev/null 2>&1; then
  base_ref="main"
else
  exit 0
fi
base=$(git merge-base "$base_ref" HEAD 2>/dev/null || true)
[ -z "$base" ] && exit 0

# Escape hatch: a stated deliberate weakening anywhere in this branch's commit messages.
# NB: read the marker with grep -F (NOT grep -q). Under `set -o pipefail`, grep -q closes
# the pipe on its first match — and the marker rides the newest commit, emitted FIRST — so
# git log, still streaming the rest of the branch's messages, dies with SIGPIPE (141) and
# pipefail turns the whole condition non-zero, silently skipping the hatch on any branch
# whose log exceeds the pipe buffer. grep -F drains all input, so no early close, no SIGPIPE.
if [ "$base" != "$(git rev-parse HEAD)" ] && \
   [ -n "$(git log --format='%B' "$base"..HEAD 2>/dev/null | grep -F '[allow-test-weakening]' || true)" ]; then
  exit 0
fi

# Test files changed on this branch (committed-ahead + working tree) vs the base.
# while-read (not mapfile) so this runs under macOS's bash 3.2.
files=()
while IFS= read -r line; do
  [ -n "$line" ] && files+=("$line")
done < <(git diff --name-only "$base" -- '*_test.go' '*.test.ts' '*.test.tsx' '*.spec.ts' '*.spec.tsx' 2>/dev/null || true)
[ ${#files[@]} -eq 0 ] && exit 0

ASSERT_RE='t\.(Fatal|Fatalf|Error|Errorf)\b|\b(require|assert)\.|expect\('
WEAKEN_RE='\bt\.Skip(Now)?\b|\.(skip|only)\(|\bxit\b|\bxdescribe\b|\bos\.Exit\b|\brecover\(\)'

total_removed=0
total_added=0
report=""

for f in "${files[@]}"; do
  [ -f "$f" ] || { report+="  $f: test file DELETED (all its coverage removed)\n"; continue; }
  fdiff=$(git diff "$base" -- "$f" 2>/dev/null || true)
  [ -z "$fdiff" ] && continue

  # Added/removed lines only (skip the +++/--- headers).
  added_lines=$(printf '%s\n' "$fdiff" | grep -E '^\+' | grep -vE '^\+\+\+' || true)
  removed_lines=$(printf '%s\n' "$fdiff" | grep -E '^-' | grep -vE '^---' || true)

  r=$(printf '%s\n' "$removed_lines" | grep -cE "$ASSERT_RE" || true)
  a=$(printf '%s\n' "$added_lines" | grep -cE "$ASSERT_RE" || true)
  total_removed=$((total_removed + r))
  total_added=$((total_added + a))
  if [ "$r" -gt "$a" ]; then
    report+="  $f: assertions ${a} added / ${r} removed (net -$((r - a)))\n"
  fi

  weak=$(printf '%s\n' "$added_lines" | grep -nE "$WEAKEN_RE" | sed 's/^/      /' || true)
  if [ -n "$weak" ]; then
    report+="  $f: added a skip/only/exit/recover:\n${weak}\n"
  fi
done

fail=0
if [ "$total_removed" -gt "$total_added" ]; then
  fail=1
fi
[ -n "$report" ] && fail=1

if [ "$fail" -ne 0 ]; then
  echo "check-test-integrity: this branch's test changes SHED strength (assertions removed,"
  echo "or a skip/only/exit added). A fix should add or hold assertions, not lose them."
  echo "Net assertions across changed test files: ${total_added} added / ${total_removed} removed."
  [ -n "$report" ] && printf '%b' "$report"
  echo "If the removal is deliberate (a genuinely-wrong assertion, a retired test), state it:"
  echo "  put  [allow-test-weakening]  in a commit message on this branch, with why."
  exit 1
fi

exit 0
