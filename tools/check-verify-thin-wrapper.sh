#!/usr/bin/env bash
set -euo pipefail

# scripts/verify.sh is the terminal front door to the verify checks; it MUST stay a thin
# wrapper that delegates to scripts/stop-checks.sh (in --cli mode) and reimplement NONE of
# the checks itself. That is the whole reason verify is a second script rather than a
# second copy: one copy of the checks means the hook path and the terminal path can never
# drift (the exact class of bug the tools/check-*-parity.sh guards exist to prevent).
#
# This guard fails if verify.sh stops delegating to stop-checks.sh, or if any check runner
# leaks into it (a sign someone started duplicating checks here). Exit 0 clean, 1 with a
# report.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
VERIFY="$REPO_ROOT/scripts/verify.sh"
STOP="$REPO_ROOT/scripts/stop-checks.sh"

if [ ! -f "$VERIFY" ] || [ ! -f "$STOP" ]; then
  echo "check-verify-thin-wrapper: scripts/verify.sh and scripts/stop-checks.sh must both exist"
  exit 1
fi

# 1. verify.sh must delegate to stop-checks.sh.
if ! grep -q 'stop-checks\.sh' "$VERIFY"; then
  echo "check-verify-thin-wrapper: scripts/verify.sh no longer calls stop-checks.sh — it must"
  echo "delegate, not reimplement. There must be exactly ONE copy of the checks."
  exit 1
fi

# 2. No check runner may appear in verify.sh — those live ONLY in stop-checks.sh. A hit here
#    means checks are being duplicated into verify.sh, the drift this guard prevents.
leak=$(grep -nE 'tools/check-|go (build|test|vet)\b|npm run|staticcheck|eslint|vitest|tsc ' "$VERIFY" || true)
if [ -n "$leak" ]; then
  echo "check-verify-thin-wrapper: scripts/verify.sh must not run checks itself — it is a thin"
  echo "wrapper on stop-checks.sh (one copy, no drift). Move these back to stop-checks.sh:"
  echo "$leak"
  exit 1
fi

exit 0
