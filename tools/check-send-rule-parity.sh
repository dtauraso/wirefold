#!/usr/bin/env bash
set -euo pipefail

# Verifies that the SendRule string constants declared anywhere in the
# nodes/Wiring/ Go package match the SEND_RULES array in
# tools/topology-vscode/src/schema/types.ts.
# Both sides are hand-maintained; this guard fails on any divergence.
# Exit 0 if clean; exit 1 with a report if they diverge.
#
# NOTE: SendRule consts are not confined to one file (ports.go today) — a
# future file split (e.g. ports_extra.go) could add a const elsewhere in the
# same package. Scrape ALL tracked .go files under nodes/Wiring/, not one
# hardcoded filename, so the guard can't go blind on a split.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

WIRING_DIR="$REPO_ROOT/nodes/Wiring"
TYPES_TS="$REPO_ROOT/tools/topology-vscode/src/schema/types.ts"

if [[ ! -d "$WIRING_DIR" ]]; then
  echo "send-rule-parity: MISCONFIGURED — dir not found: $WIRING_DIR" >&2
  exit 1
fi
if [[ ! -f "$TYPES_TS" ]]; then
  echo "send-rule-parity: MISCONFIGURED — file not found: $TYPES_TS" >&2
  exit 1
fi

# Extract SendRule const values from every tracked .go file in nodes/Wiring/:
#   lines of the form:  RuleFoo SendRule = "someValue"
rules_from_go() {
  local go_files
  go_files=$(cd "$REPO_ROOT" && git ls-files 'nodes/Wiring/*.go')
  [[ -z "$go_files" ]] && return
  ( cd "$REPO_ROOT" && grep -haE 'SendRule[[:space:]]*=[[:space:]]*"[^"]+"' $go_files ) \
    | grep -o '"[^"]*"' \
    | tr -d '"' \
    | sort
}

# Extract values from the SEND_RULES array in types.ts:
#   export const SEND_RULES: readonly SendRule[] = ["consumeGated", "fireAndForget"];
rules_from_ts() {
  grep -a 'SEND_RULES' "$TYPES_TS" \
    | grep -o '"[^"]*"' \
    | tr -d '"' \
    | sort
}

# NOTE `|| true` on every extractor assignment below. Without it, `set -euo pipefail` kills
# the script AT THE ASSIGNMENT whenever an extractor's grep legitimately matches nothing —
# so the assert_nonempty diagnostic underneath, which exists precisely to explain that case,
# could never print. The script still exited nonzero, so it failed SAFE but SILENTLY,
# defeating the message. Verified with a minimal repro.
GO_RULES=$(rules_from_go) || true
TS_RULES=$(rules_from_ts) || true

# Refuse a vacuous pass: if either extractor returns an EMPTY set (a SendRule const
# rename in ports.go or a SEND_RULES rename in types.ts), comm would compare
# empty-to-empty and "pass" blind. Assert each set is non-empty. (Positive-assertion
# pattern, per check-edit-op-parity.sh / check-message-kind-parity.sh.)
assert_nonempty() { # value label
  if [[ -z "$(printf '%s' "$1" | tr -d '[:space:]')" ]]; then
    echo "send-rule-parity: EMPTY extracted set for '$2' — const/array missing or renamed; refusing vacuous parity pass" >&2
    exit 1
  fi
}
assert_nonempty "$GO_RULES" "SendRule consts (ports.go)"
assert_nonempty "$TS_RULES" "SEND_RULES array (types.ts)"

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
