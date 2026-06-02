#!/usr/bin/env bash
# check-dead-doc-tokens.sh — fail if retired architecture tokens reappear in CLAUDE.md or MODEL.md.
# Run from repo root: bash scripts/check-dead-doc-tokens.sh
set -u
cd "$(git rev-parse --show-toplevel)" || exit 1

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
  for token in "${DEAD_TOKENS[@]}"; do
    if grep -qF "$token" "$doc" 2>/dev/null; then
      echo "DEAD TOKEN: '$token' found in $doc — remove or update the reference"
      fail=1
    fi
  done
done

exit $fail
