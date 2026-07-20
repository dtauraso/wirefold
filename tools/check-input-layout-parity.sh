#!/usr/bin/env bash
set -euo pipefail

# Verifies the INPUT_LAYOUT_FINGERPRINT embedded in the Go and TS input-record codecs are
# identical, confirming both sides encode/decode the SAME editor→Go binary record layout
# (record kinds, field order, enum orderings). The TS→Go bridge is a binary buffer; a
# fingerprint drift means the webview would encode a record Go decodes wrong (silent
# mis-dispatch), so this guard fails on any divergence.
#
# The fingerprint is hand-maintained in BOTH files and must be bumped on both sides for any
# layout change:
#   nodes/Wiring/input_codec.go                         (InputLayoutFingerprint / comment)
#   tools/topology-vscode/src/schema/input-layout.ts    (INPUT_LAYOUT_FINGERPRINT / comment)
#
# Exit 0 if clean; exit 1 with a report if the fingerprints diverge.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

GO_FILE="$REPO_ROOT/nodes/Wiring/input_codec.go"
TS_FILE="$REPO_ROOT/tools/topology-vscode/src/schema/input-layout.ts"

for f in "$GO_FILE" "$TS_FILE"; do
  if [[ ! -f "$f" ]]; then
    echo "check-input-layout-parity: MISCONFIGURED — file not found: $f" >&2
    exit 1
  fi
done

# Two values live in each file and BOTH are checked:
#
#   comment — the "INPUT_LAYOUT_FINGERPRINT:" marker line (human-readable)
#   const   — the actual string constant the codec compiles against (the wire contract)
#
# Reading only the comment was the original bug: the comments could agree while the
# CONSTANTS diverged, and the guard reported clean. A comment is not the contract.
# Verified by injecting drift into each field independently — both now fail.
fingerprint_comment() { # file
  grep -a 'INPUT_LAYOUT_FINGERPRINT:' "$1" \
    | head -n1 \
    | sed 's/.*INPUT_LAYOUT_FINGERPRINT: //'
}

# The const spans a line break in TS (`export const X =\n  "v18 …";`) and is single-line in
# Go, so join lines before extracting the first quoted string after the identifier.
fingerprint_const() { # file
  tr '\n' ' ' < "$1" \
    | sed -E 's/.*(INPUT_LAYOUT_FINGERPRINT|InputLayoutFingerprint)[[:space:]]*=[[:space:]]*"//; s/".*//' \
    | head -n1
}

assert_nonempty() { # value label
  if [[ -z "$(printf '%s' "$1" | tr -d '[:space:]')" ]]; then
    echo "check-input-layout-parity: EMPTY fingerprint for '$2' — marker comment missing or file renamed; refusing vacuous parity pass" >&2
    exit 1
  fi
}

FP_GO=$(fingerprint_comment "$GO_FILE")
FP_TS=$(fingerprint_comment "$TS_FILE")
CONST_GO=$(fingerprint_const "$GO_FILE")
CONST_TS=$(fingerprint_const "$TS_FILE")

assert_nonempty "$FP_GO" "input_codec.go comment"
assert_nonempty "$FP_TS" "input-layout.ts comment"
assert_nonempty "$CONST_GO" "input_codec.go const"
assert_nonempty "$CONST_TS" "input-layout.ts const"

# A file whose comment contradicts its own const is already broken, regardless of whether
# the two files happen to agree — catch it before the cross-file compare.
for pair in "input_codec.go|$FP_GO|$CONST_GO" "input-layout.ts|$FP_TS|$CONST_TS"; do
  IFS='|' read -r label c k <<< "$pair"
  if [[ "$c" != "$k" ]]; then
    echo "check-input-layout-parity: $label — marker COMMENT and CONST disagree"
    echo "    comment: $c"
    echo "    const:   $k"
    echo ""
    echo "The const is the wire contract; bump the comment to match it."
    exit 1
  fi
done

if [[ "$FP_GO" != "$FP_TS" ]]; then
  echo "check-input-layout-parity: Go and TS input-record fingerprints DIVERGE"
  echo ""
  echo "  Go  ($GO_FILE):"
  echo "    $FP_GO"
  echo ""
  echo "  TS  ($TS_FILE):"
  echo "    $FP_TS"
  echo ""
  echo "Bump both fingerprints together after any record-layout change."
  exit 1
fi

echo "check-input-layout-parity: clean (fingerprint matches)"
exit 0
