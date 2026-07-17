#!/usr/bin/env bash
set -euo pipefail

# Verifies that the BUF_LAYOUT_FINGERPRINT embedded in the two GENERATED
# buffer-layout files is identical:
#   Buffer/buffer_layout_gen.go
#   tools/topology-vscode/src/schema/buffer-layout.ts
#
# SCOPE — read this before trusting the guard's name. Both files are written by ONE
# generator run (tools/gen-node-defs) from Buffer/layout.go, with the SAME fingerprint
# line. So this guard is generated-vs-generated: it stays clean whenever the two agree,
# which is any state produced by a full regen — INCLUDING a stale regen where layout.go
# changed but neither file was regenerated (both stay matching). It therefore does NOT
# catch an unregenerated layout.go column; check-generated.sh does (it regenerates from
# layout.go and diffs). What THIS guard uniquely catches is the two generated files
# DISAGREEING — a botched/partial regen that touched one file, a hand-edited fingerprint,
# or a deleted checked-in generated Go file (which check-generated silently recreates).
# It never reads layout.go, and a generated reader existing for a column says nothing
# about whether any production code CONSUMES it (dead columns can pass every guard).
#
# Exit 0 if clean; exit 1 with a report if the fingerprints diverge.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

GO_FILE="$REPO_ROOT/Buffer/buffer_layout_gen.go"
TS_FILE="$REPO_ROOT/tools/topology-vscode/src/schema/buffer-layout.ts"

for f in "$GO_FILE" "$TS_FILE"; do
  if [[ ! -f "$f" ]]; then
    echo "check-buffer-layout-parity: MISCONFIGURED — file not found: $f" >&2
    exit 1
  fi
done

# Extract the fingerprint value (everything after "BUF_LAYOUT_FINGERPRINT: ").
# -a/--text: force text mode so BSD grep doesn't classify binary-looking content as binary.
fingerprint_go() {
  grep -a 'BUF_LAYOUT_FINGERPRINT:' "$GO_FILE" \
    | sed 's/.*BUF_LAYOUT_FINGERPRINT: //'
}

fingerprint_ts() {
  grep -a 'BUF_LAYOUT_FINGERPRINT:' "$TS_FILE" \
    | sed 's/.*BUF_LAYOUT_FINGERPRINT: //'
}

# Refuse a vacuous pass: an empty extraction means the fingerprint sentinel is
# missing (file renamed, comment stripped, etc.). Assert non-empty.
assert_nonempty() { # value label
  if [[ -z "$(printf '%s' "$1" | tr -d '[:space:]')" ]]; then
    echo "check-buffer-layout-parity: EMPTY fingerprint for '$2' — BUF_LAYOUT_FINGERPRINT comment missing or file renamed; refusing vacuous parity pass" >&2
    exit 1
  fi
}

FP_GO=$(fingerprint_go)
FP_TS=$(fingerprint_ts)

assert_nonempty "$FP_GO" "Buffer/buffer_layout_gen.go"
assert_nonempty "$FP_TS" "buffer-layout.ts"

if [[ "$FP_GO" != "$FP_TS" ]]; then
  echo "check-buffer-layout-parity: Go and TS buffer layout fingerprints DIVERGE"
  echo ""
  echo "  Go  ($GO_FILE):"
  echo "    $FP_GO"
  echo ""
  echo "  TS  ($TS_FILE):"
  echo "    $FP_TS"
  echo ""
  echo "Regenerate with: cd tools/topology-vscode && npm run gen:node-defs"
  exit 1
fi

echo "check-buffer-layout-parity: clean (fingerprint matches)"
exit 0
