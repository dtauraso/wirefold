#!/usr/bin/env bash
# check-dead-doc-tokens.sh — fail if retired architecture tokens reappear in CLAUDE.md or MODEL.md.
# Run from repo root: bash tools/check-dead-doc-tokens.sh
set -euo pipefail

# Lives in tools/ with every other guard, and resolves the repo root the way they all do
# (SCRIPT_DIR/REPO_ROOT). It used to sit in scripts/ and cd via git rev-parse — which forced
# stop-checks to invoke it as a special case OUTSIDE its guard loop, purely because of the
# directory. Repo-root resolution was being done two different ways depending on where a
# script happened to live.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

DOCS=(CLAUDE.md MODEL.md)

# Tokens that were retired when React Flow was replaced by the three/ renderer.
DEAD_TOKENS=(
  "rf/nodes"
  "GenericNode"
  "PUMP_SLOT_HANDLER"
  "webview/schema/"
  "webview/rf/"
)

fail=0
for doc in "${DOCS[@]}"; do
  if [ ! -f "$doc" ]; then
    echo "dead-doc-tokens: MISCONFIGURED — doc not found: $doc (renamed? a missing doc would vacuously pass)" >&2
    exit 1
  fi
  for token in "${DEAD_TOKENS[@]}"; do
    if grep -aqF "$token" "$doc" 2>/dev/null; then
      echo "DEAD TOKEN: '$token' found in $doc — remove or update the reference"
      fail=1
    fi
  done
done

exit $fail
