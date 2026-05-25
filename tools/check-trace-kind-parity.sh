#!/usr/bin/env bash
set -euo pipefail

# Verifies that the kind literals in TRACE_EVENT_KINDS (trace-kinds.ts) and the
# case labels in pump.ts's trace switch are identical sets.
# Exit 0 if clean; exit 1 with a report if they diverge.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

TRACE_KINDS_FILE="$REPO_ROOT/tools/topology-vscode/src/webview/rf/trace-kinds.ts"
PUMP_FILE="$REPO_ROOT/tools/topology-vscode/src/webview/rf/pump.ts"

# Extract kinds from TRACE_EVENT_KINDS array (quoted string literals on that line).
kinds_from_ts() {
  grep 'TRACE_EVENT_KINDS' "$TRACE_KINDS_FILE" \
    | grep -o '"[^"]*"' \
    | tr -d '"' \
    | sort
}

# Extract case labels from pump.ts (lines of the form: case "...":)
kinds_from_pump() {
  grep -E '^\s*case "[^"]+":' "$PUMP_FILE" \
    | grep -o '"[^"]*"' \
    | tr -d '"' \
    | sort
}

TS_KINDS=$(kinds_from_ts)
PUMP_KINDS=$(kinds_from_pump)

# comm -23: in TS only (missing from pump); comm -13: in pump only (extra vs TS)
MISSING=$(comm -23 <(echo "$TS_KINDS") <(echo "$PUMP_KINDS"))
EXTRA=$(comm -13 <(echo "$TS_KINDS") <(echo "$PUMP_KINDS"))

HITS=0
if [[ -n "$MISSING" ]]; then
  echo "trace-kind-parity: kinds in TRACE_EVENT_KINDS but missing from pump.ts switch:"
  while IFS= read -r k; do
    echo "  missing case: \"$k\""
    HITS=$((HITS + 1))
  done <<< "$MISSING"
fi

if [[ -n "$EXTRA" ]]; then
  echo "trace-kind-parity: case labels in pump.ts switch not in TRACE_EVENT_KINDS:"
  while IFS= read -r k; do
    echo "  extra case: \"$k\""
    HITS=$((HITS + 1))
  done <<< "$EXTRA"
fi

if [[ $HITS -eq 0 ]]; then
  echo "trace-kind-parity: clean"
  exit 0
fi

echo ""
echo "trace-kind-parity: $HITS divergence(s) found"
exit 1
