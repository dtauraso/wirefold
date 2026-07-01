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
    # Extract case literals from ONLY the `switch msg.Type` block. Bound the window
    # to that switch (stop at the next `switch`), so nested op/sub-kind switches like
    # `switch vp.Kind` (the op="viewpoint" payload kinds set/orbit/zoom/...) are NOT
    # mistaken for top-level message types. (A fixed -A window spilled into them.)
    awk '
      /switch[[:space:]]+msg\.Type/ { inblk=1; next }
      inblk && /switch[[:space:]]/  { inblk=0 }
      inblk
    ' "$STDIN_READER" \
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

# Refuse a vacuous pass: if either extractor returns an EMPTY set (the switch/const was
# renamed or removed), comm would compare empty-to-empty and "pass". Both sides must be
# non-empty. (Positive-assertion pattern, per check-ts-shading-from-go.sh.)
for pair in "GO_KINDS:stdin_reader.go msg.Type kinds" "TS_KINDS:WEBVIEW_TO_HOST_TYPES kinds"; do
  var="${pair%%:*}"; label="${pair#*:}"
  if [[ -z "$(printf '%s' "${!var}" | tr -d '[:space:]')" ]]; then
    echo "message-kind-parity: EMPTY extracted set for '$label' — switch/const missing or renamed; refusing vacuous parity pass" >&2
    exit 1
  fi
done

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
