#!/usr/bin/env bash
# check-gofmt.sh — fail if any tracked Go file is not gofmt-formatted.
#
# gofmt is the canonical Go formatter; the rest of the toolchain assumes it.
# Formatting drift accumulated silently because nothing in the guard suite
# checked it (four cleanup passes each had to note pre-existing drift as
# out-of-scope). This guard closes that: run `gofmt -w .` to fix.
#
# Whole-repo scan (excludes vendor/node_modules); sub-second, so it runs
# unconditionally rather than diff-gated.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

# gofmt -l lists files whose formatting differs from gofmt's. Scan the repo,
# excluding dependency/build dirs that aren't ours.
unformatted="$(gofmt -l . 2>/dev/null | grep -vE '(^|/)(vendor|node_modules)/' || true)"

if [ -n "$unformatted" ]; then
  echo "gofmt: the following Go files are not formatted (run 'gofmt -w .'):" >&2
  echo "$unformatted" >&2
  exit 1
fi

echo "check-gofmt: clean"
