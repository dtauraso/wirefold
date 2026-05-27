#!/usr/bin/env bash
set -euo pipefail

# Verifies that every message-type discriminator recognized by Go's
# stdin_reader.go (the Go↔TS webview-to-host seam) is also declared in
# WEBVIEW_TO_HOST_TYPES in messages.ts.
# Exit 0 if clean; exit 1 with a report if they diverge.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

STDIN_READER="$REPO_ROOT/nodes/Wiring/stdin_reader.go"
MESSAGES_TS="$REPO_ROOT/tools/topology-vscode/src/messages.ts"

# Extract string literals compared against msg.Type in stdin_reader.go.
# Patterns matched:
#   msg.Type != "..." or msg.Type == "..."  (if-style comparisons)
#   case "...":  inside a switch msg.Type block
kinds_from_go() {
  {
    grep -oE 'msg\.Type[[:space:]]*[!=]=[[:space:]]*"[^"]+"' "$STDIN_READER" \
      | grep -oE '"[^"]+"' \
      | tr -d '"'
    # Extract case literals from switch msg.Type blocks.
    grep -A 200 'switch msg\.Type' "$STDIN_READER" \
      | grep -oE 'case[[:space:]]+"[^"]+"' \
      | grep -oE '"[^"]+"' \
      | tr -d '"'
  } | sort -u
}

# Extract the string literals inside WEBVIEW_TO_HOST_TYPES in messages.ts.
# The const spans multiple lines; awk slurps from the declaration to the closing ]);
# then we drop the spread line, the declaration line, and the closing line.
kinds_from_ts() {
  awk '/WEBVIEW_TO_HOST_TYPES/,/\]\)/' "$MESSAGES_TS" \
    | grep -vE 'flatMap|WEBVIEW_TO_HOST_TYPES|\]\)' \
    | grep -o '"[^"]*"' \
    | tr -d '"' \
    | sort -u
}

GO_KINDS=$(kinds_from_go)
TS_KINDS=$(kinds_from_ts)

MISSING=$(comm -23 <(echo "$GO_KINDS") <(echo "$TS_KINDS"))
EXTRA=$(comm -13 <(echo "$GO_KINDS") <(echo "$TS_KINDS"))

HITS=0
if [[ -n "$MISSING" ]]; then
  echo "message-kind-parity: kinds in stdin_reader.go but missing from WEBVIEW_TO_HOST_TYPES:"
  while IFS= read -r k; do
    echo "  missing: \"$k\""
    HITS=$((HITS + 1))
  done <<< "$MISSING"
fi

# Extra TS kinds that Go doesn't recognize are fine (TS handles more message
# types than stdin_reader.go), so we only report Go→TS missing, not TS→Go extra.
# Uncomment the block below if you want strict bidirectional parity.
#
# if [[ -n "$EXTRA" ]]; then
#   echo "message-kind-parity: kinds in WEBVIEW_TO_HOST_TYPES not matched in stdin_reader.go:"
#   while IFS= read -r k; do
#     echo "  extra: \"$k\""
#     HITS=$((HITS + 1))
#   done <<< "$EXTRA"
# fi

if [[ $HITS -eq 0 ]]; then
  echo "message-kind-parity: clean"
  exit 0
fi

echo ""
echo "message-kind-parity: $HITS divergence(s) found"
exit 1
