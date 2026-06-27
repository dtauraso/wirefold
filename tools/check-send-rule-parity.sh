#!/usr/bin/env bash
set -euo pipefail

# Verifies that the SendRule string constants in nodes/Wiring/ports.go match
# the SEND_RULES array in tools/topology-vscode/src/schema/types.ts.
# Both sides are hand-maintained; this guard fails on any divergence.
# Exit 0 if clean; exit 1 with a report if they diverge.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

PORTS_GO="$REPO_ROOT/nodes/Wiring/ports.go"
TYPES_TS="$REPO_ROOT/tools/topology-vscode/src/schema/types.ts"

for f in "$PORTS_GO" "$TYPES_TS"; do
  if [[ ! -f "$f" ]]; then
    echo "send-rule-parity: MISCONFIGURED — file not found: $f" >&2
    exit 1
  fi
done

# Extract SendRule const values from Go:
#   lines of the form:  RuleFoo SendRule = "someValue"
rules_from_go() {
  grep -E 'SendRule\s*=\s*"[^"]+"' "$PORTS_GO" \
    | grep -o '"[^"]*"' \
    | tr -d '"' \
    | sort
}

# Extract values from the SEND_RULES array in types.ts:
#   export const SEND_RULES: readonly SendRule[] = ["consumeGated", "fireAndForget"];
rules_from_ts() {
  grep 'SEND_RULES' "$TYPES_TS" \
    | grep -o '"[^"]*"' \
    | tr -d '"' \
    | sort
}

GO_RULES=$(rules_from_go)
TS_RULES=$(rules_from_ts)

MISSING=$(comm -23 <(echo "$GO_RULES") <(echo "$TS_RULES"))
EXTRA=$(comm -13 <(echo "$GO_RULES") <(echo "$TS_RULES"))

HITS=0
if [[ -n "$MISSING" ]]; then
  while IFS= read -r k; do
    [[ -z "$k" ]] && continue
    echo "  SendRule in ports.go but missing from SEND_RULES in types.ts: \"$k\""
    HITS=$((HITS + 1))
  done <<< "$MISSING"
fi

if [[ -n "$EXTRA" ]]; then
  while IFS= read -r k; do
    [[ -z "$k" ]] && continue
    echo "  SendRule in SEND_RULES (types.ts) but not defined in ports.go: \"$k\""
    HITS=$((HITS + 1))
  done <<< "$EXTRA"
fi

if [[ $HITS -eq 0 ]]; then
  echo "send-rule-parity: clean"
  exit 0
fi

echo ""
echo "send-rule-parity: $HITS divergence(s) found"
exit 1
