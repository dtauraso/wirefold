#!/usr/bin/env bash
# check-kind-imports: kinds_generated.go must match what tools/gen-kind-imports produces.
#
# WHY THIS EXISTS: kinds_generated.go holds the blank imports (_ "…/nodes/hold") that make
# each node package's init() run, and init() is what calls Wiring.Register. No import means
# the kind does not exist in the binary — it fails at runtime with `unknown type "X"` even
# though its SPEC.md, Go package, and NODE_DEFS entry are all correct. Before this guard,
# nothing regenerated or verified the file: `grep -rl gen-kind-imports tools/ scripts/`
# matched only the generator's own source, and a hand-edited kinds_generated.go passed the
# full stop-checks suite clean.
#
# check-generated.sh does NOT cover this file: it derives its file list from gen-node-defs'
# "wrote <path>" output, and gen-kind-imports is a different generator that nothing invoked.
#
# Companion: kind_registry_parity_test.go catches the same failure from the other side (a
# kind in the generated table that never registered). This guard catches it earlier and
# says which command to run.
set -euo pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$REPO_ROOT"

TARGET="kinds_generated.go"

# The generator must be able to tell us what it wrote; parse it rather than hardcoding, so
# a rename of the output file cannot make this guard silently check a path that no longer
# exists (the split-drift failure mode: see memory/feedback_guards_hardcoding_single_file_break_on_split.md).
GEN_OUT=$(go run ./tools/gen-kind-imports 2>&1) || {
  echo "check-kind-imports: generator failed" >&2
  echo "$GEN_OUT" >&2
  exit 1
}

# "gen-kind-imports: wrote /abs/path/kinds_generated.go (8 kinds)" -> repo-relative path
WROTE=$(printf '%s\n' "$GEN_OUT" \
  | sed -nE 's|^gen-kind-imports: wrote ([^ ]+).*$|\1|p' \
  | sed "s|^$REPO_ROOT/||" \
  | sort -u)

# Refuse a vacuous pass: no parsed path means the generator's output format changed and
# this guard would be checking NOTHING while reporting clean.
if [[ -z "$WROTE" ]]; then
  echo "check-kind-imports: MISCONFIGURED — parsed 0 written files from the generator output." >&2
  echo "  Its 'wrote <path>' format likely changed; this guard would silently check nothing." >&2
  echo "  Generator said:" >&2
  printf '%s\n' "$GEN_OUT" | sed 's/^/    /' >&2
  exit 1
fi

# The parsed path must be the file we think we are guarding. If the generator starts writing
# somewhere else, fail loudly rather than guard a stale path.
if [[ "$WROTE" != "$TARGET" ]]; then
  echo "check-kind-imports: MISCONFIGURED — generator wrote '$WROTE', expected '$TARGET'." >&2
  echo "  Update TARGET in this guard to match the generator." >&2
  exit 1
fi

# An untracked generated file can never be reported stale by git status — it would pass
# vacuously forever.
if ! git ls-files --error-unmatch "$TARGET" >/dev/null 2>&1; then
  echo "check-kind-imports: generated file is not tracked by git: $TARGET" >&2
  echo "  (an untracked generated file can never be reported stale — commit it)" >&2
  exit 1
fi

stale=$(git status --porcelain -- "$TARGET" 2>/dev/null || true)
if [ -n "$stale" ]; then
  echo "check-kind-imports: $TARGET is stale — commit the regenerated output:"
  echo "$stale"
  echo
  echo "  A kind reaches Wiring.Register ONLY via this file's blank import. Stale here means"
  echo "  the kind does not exist in the binary and fails at runtime with: unknown type \"X\"."
  echo "  Regenerate with: go run ./tools/gen-kind-imports"
  exit 1
fi

echo "check-kind-imports: clean ($TARGET matches gen-kind-imports)"
exit 0
