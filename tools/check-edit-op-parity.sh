#!/usr/bin/env bash
set -euo pipefail

# Verifies that every "edit" op declared in the EditMsg union in messages.ts
# (the TS→Go geometry-CRUD axis) is handled by applyEdit in stdin_reader.go,
# and vice versa. The top-level message-TYPE parity is covered by
# check-message-kind-parity.sh; this guards the internal op axis, which that
# check is blind to. An op added on one side and forgotten on the other
# silently no-ops at runtime (CLAUDE.md "Bridge surface": a new TS→Go message
# kind is one top-level `edit` op, kept in message-kind parity with the Go
# stdin reader).
# Exit 0 if clean; exit 1 with a report if they diverge.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

STDIN_READER="$REPO_ROOT/nodes/Wiring/stdin_reader.go"
MESSAGES_TS="$REPO_ROOT/tools/topology-vscode/src/messages.ts"

# Ops from the TS EditMsg union: `op: "..."` literals in messages.ts.
ops_from_ts() {
  grep -oE 'op: "[^"]+"' "$MESSAGES_TS" \
    | grep -oE '"[^"]+"' \
    | tr -d '"' \
    | sort -u
}

# Ops handled by applyEdit in stdin_reader.go: `msg.Op == "..."` case literals.
ops_from_go() {
  grep -oE 'msg\.Op == "[^"]+"' "$STDIN_READER" \
    | grep -oE '"[^"]+"' \
    | tr -d '"' \
    | sort -u
}

TS_OPS=$(ops_from_ts)
GO_OPS=$(ops_from_go)

MISSING_IN_GO=$(comm -23 <(echo "$TS_OPS") <(echo "$GO_OPS"))
MISSING_IN_TS=$(comm -13 <(echo "$TS_OPS") <(echo "$GO_OPS"))

HITS=0
if [[ -n "$MISSING_IN_GO" ]]; then
  echo "edit-op-parity: ops in messages.ts EditMsg but not handled by applyEdit in stdin_reader.go:"
  while IFS= read -r o; do
    echo "  unhandled in Go: \"$o\""
    HITS=$((HITS + 1))
  done <<< "$MISSING_IN_GO"
fi
if [[ -n "$MISSING_IN_TS" ]]; then
  echo "edit-op-parity: ops handled by applyEdit in stdin_reader.go but not declared in messages.ts EditMsg:"
  while IFS= read -r o; do
    echo "  undeclared in TS: \"$o\""
    HITS=$((HITS + 1))
  done <<< "$MISSING_IN_TS"
fi

if [[ $HITS -eq 0 ]]; then
  echo "edit-op-parity: clean"
  exit 0
fi

echo ""
echo "edit-op-parity: $HITS divergence(s) found"
exit 1
