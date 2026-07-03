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

# Extract the value after the "INPUT_LAYOUT_FINGERPRINT:" marker comment (present in both
# files). Take the first match only.
fingerprint() { # file
  grep -a 'INPUT_LAYOUT_FINGERPRINT:' "$1" \
    | head -n1 \
    | sed 's/.*INPUT_LAYOUT_FINGERPRINT: //'
}

assert_nonempty() { # value label
  if [[ -z "$(printf '%s' "$1" | tr -d '[:space:]')" ]]; then
    echo "check-input-layout-parity: EMPTY fingerprint for '$2' — marker comment missing or file renamed; refusing vacuous parity pass" >&2
    exit 1
  fi
}

FP_GO=$(fingerprint "$GO_FILE")
FP_TS=$(fingerprint "$TS_FILE")

assert_nonempty "$FP_GO" "input_codec.go"
assert_nonempty "$FP_TS" "input-layout.ts"

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
