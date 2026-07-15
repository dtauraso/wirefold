#!/usr/bin/env bash
set -euo pipefail

# Verifies TWO parities for the editor→Go seam:
#   1. every message type dispatched by stdin_reader.go is declared in
#      WEBVIEW_TO_HOST_TYPES in messages.ts;
#   2. every message type dispatched by stdin_reader.go is DOCUMENTED in that file's own
#      MSG_TYPES_DOC block, and vice versa — so the header cannot undercount its switch.
# Exit 0 if clean; exit 1 with a report if they diverge.
#
# (2) exists because the header once enumerated five types while the switch had seven; the
# undocumented one (fade-toggle) then read as contradicting the header. Prose describing a
# dispatch is only true if something fails when it stops being true.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

STDIN_READER="$REPO_ROOT/nodes/Wiring/stdin_reader.go"
MESSAGES_TS="$REPO_ROOT/tools/topology-vscode/src/messages.ts"

for f in "$STDIN_READER" "$MESSAGES_TS"; do
  if [[ ! -f "$f" ]]; then
    echo "message-kind-parity: MISCONFIGURED — file not found: $f" >&2
    exit 1
  fi
done

# Extract string literals compared against msg.Type in stdin_reader.go.
# Patterns matched:
#   msg.Type != "..." or msg.Type == "..."  (if-style comparisons)
#   case "...":  inside the MSG_TYPES_START/END fence
kinds_from_go() {
  {
    grep -aoE 'msg\.Type[[:space:]]*[!=]=[[:space:]]*"[^"]+"' "$STDIN_READER" \
      | grep -oE '"[^"]+"' \
      | tr -d '"'
    # Extract case literals from the FENCED dispatch switch only. The fence is explicit
    # rather than a heuristic window, so nested op/sub-kind switches can never be mistaken
    # for top-level types. Same pattern as EDIT_OPS_START/END in applyEdit.
    #
    # Markers are matched ANCHORED (a comment line containing the marker and nothing else).
    # An unanchored match is a trap: this file's own header PROSE names the markers, and a
    # loose /MARKER/ match opens the fence on that prose line — which made a deleted marker
    # still "pass". Anchoring is what makes deleting the fence fail loudly.
    awk '
      /^[[:space:]]*\/\/[[:space:]]*MSG_TYPES_START[[:space:]]*$/ { inblk=1; next }
      /^[[:space:]]*\/\/[[:space:]]*MSG_TYPES_END[[:space:]]*$/   { inblk=0 }
      inblk
    ' "$STDIN_READER" \
      | grep -aoE 'case[[:space:]]+"[^"]+"' \
      | grep -oE '"[^"]+"' \
      | tr -d '"'
  } | sort -u
}

# Extract the types DECLARED in stdin_reader.go's own MSG_TYPES_DOC block. Only numbered
# entry lines are read (`//  N. "type" [/ "type"] — prose`), so prose on continuation lines
# can quote freely without being mistaken for a declaration.
kinds_from_go_doc() {
  awk '
    /^[[:space:]]*\/\/[[:space:]]*MSG_TYPES_DOC_START[[:space:]]*$/ { inblk=1; next }
    /^[[:space:]]*\/\/[[:space:]]*MSG_TYPES_DOC_END[[:space:]]*$/   { inblk=0 }
    inblk && /^\/\/[[:space:]]+[0-9]+\.[[:space:]]+"/
  ' "$STDIN_READER" \
    | grep -oE '"[^"]+"' \
    | tr -d '"' \
    | sort -u
}

# Extract the string literals inside WEBVIEW_TO_HOST_TYPES in messages.ts.
# The const spans multiple lines; awk slurps from the declaration to the closing ]);
# then we drop the spread line, the declaration line, and the closing line.
kinds_from_ts() {
  awk '/WEBVIEW_TO_HOST_TYPES/,/\]\)/' "$MESSAGES_TS" \
    | grep -avE 'flatMap|WEBVIEW_TO_HOST_TYPES|\]\)' \
    | grep -o '"[^"]*"' \
    | tr -d '"' \
    | sort -u
}

# NOTE `|| true` on every extractor assignment below. Without it, `set -euo pipefail` kills
# the script AT THE ASSIGNMENT whenever an extractor's grep legitimately matches nothing —
# so the assert_nonempty diagnostic underneath, which exists precisely to explain that case,
# could never print. The script still exited nonzero, so it failed SAFE but SILENTLY,
# defeating the message. Verified with a minimal repro.
GO_KINDS=$(kinds_from_go) || true
GO_DOC_KINDS=$(kinds_from_go_doc) || true
TS_KINDS=$(kinds_from_ts) || true

# Refuse a vacuous pass: if any extractor returns an EMPTY set (the switch/fence/const was
# renamed or removed), comm would compare empty-to-empty and "pass". All must be
# non-empty. (Positive-assertion pattern, per check-ts-shading-from-go.sh.)
for pair in "GO_KINDS:stdin_reader.go MSG_TYPES fenced switch" \
            "GO_DOC_KINDS:stdin_reader.go MSG_TYPES_DOC header list" \
            "TS_KINDS:WEBVIEW_TO_HOST_TYPES kinds"; do
  var="${pair%%:*}"; label="${pair#*:}"
  if [[ -z "$(printf '%s' "${!var}" | tr -d '[:space:]')" ]]; then
    echo "message-kind-parity: EMPTY extracted set for '$label' — switch/const missing or renamed; refusing vacuous parity pass" >&2
    exit 1
  fi
done

MISSING=$(comm -23 <(echo "$GO_KINDS") <(echo "$TS_KINDS"))

HITS=0
if [[ -n "$MISSING" ]]; then
  echo "message-kind-parity: kinds in stdin_reader.go but missing from WEBVIEW_TO_HOST_TYPES:"
  while IFS= read -r k; do
    echo "  missing: \"$k\""
    HITS=$((HITS + 1))
  done <<< "$MISSING"
fi

# (2) The file's own header must document exactly what it dispatches — both directions.
UNDOCUMENTED=$(comm -23 <(echo "$GO_KINDS") <(echo "$GO_DOC_KINDS"))
PHANTOM=$(comm -13 <(echo "$GO_KINDS") <(echo "$GO_DOC_KINDS"))

if [[ -n "$UNDOCUMENTED" ]]; then
  echo "message-kind-parity: types dispatched in the MSG_TYPES fence but NOT documented in MSG_TYPES_DOC:"
  while IFS= read -r k; do
    echo "  undocumented: \"$k\"  (add a numbered entry to the header)"
    HITS=$((HITS + 1))
  done <<< "$UNDOCUMENTED"
fi

if [[ -n "$PHANTOM" ]]; then
  echo "message-kind-parity: types documented in MSG_TYPES_DOC that the switch does NOT dispatch:"
  while IFS= read -r k; do
    echo "  phantom: \"$k\"  (the header describes a type that no longer exists)"
    HITS=$((HITS + 1))
  done <<< "$PHANTOM"
fi

# Extra TS kinds that Go doesn't recognize are fine (TS handles more message
# types than stdin_reader.go), so we only report Go→TS missing, not TS→Go extra.
# Uncomment the block below for strict bidirectional parity; it computes its own EXTRA
# (the live one was deleted — it was assigned on every run and read by nobody).
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
