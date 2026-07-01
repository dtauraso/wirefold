#!/usr/bin/env bash
set -euo pipefail

# Verifies that the kind literals in TRACE_EVENT_KINDS (trace-kinds.ts) and the
# case labels in pump.ts's trace switch are identical sets.
# Exit 0 if clean; exit 1 with a report if they diverge.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

TRACE_KINDS_FILE="$REPO_ROOT/tools/topology-vscode/src/schema/trace-kinds.ts"
PUMP_FILE="$REPO_ROOT/tools/topology-vscode/src/webview/three/pump.ts"

# Extract kinds from TRACE_EVENT_KINDS array (quoted string literals on that line).
kinds_from_ts() {
  grep -a 'TRACE_EVENT_KINDS' "$TRACE_KINDS_FILE" \
    | grep -o '"[^"]*"' \
    | tr -d '"' \
    | sort
}

# Extract case labels from pump.ts (lines of the form: case "...":)
kinds_from_pump() {
  # -a/--text: pump.ts contains multibyte box-drawing/arrow chars in its header
  # comments; BSD (macOS) grep classifies it as a "Binary file" and prints only
  # "Binary file ... matches" instead of the matching lines, so the extraction
  # comes back empty and assert_nonempty trips. Force text mode. [[:space:]] is
  # the POSIX class (portable across GNU/BSD grep) in place of the GNU-only \s.
  grep -aE '^[[:space:]]*case "[^"]+":' "$PUMP_FILE" \
    | grep -o '"[^"]*"' \
    | tr -d '"' \
    | sort
}

TS_KINDS=$(kinds_from_ts)
PUMP_KINDS=$(kinds_from_pump)

# Refuse a vacuous pass: an empty extraction (array/switch renamed or removed) would comm
# empty-to-empty and "pass". Assert non-empty. (Positive-assertion pattern.)
assert_nonempty() { # value label
  if [[ -z "$(printf '%s' "$1" | tr -d '[:space:]')" ]]; then
    echo "trace-kind-parity: EMPTY extracted set for '$2' — array/switch/struct missing or renamed; refusing vacuous parity pass" >&2
    exit 1
  fi
}
assert_nonempty "$TS_KINDS" "TRACE_EVENT_KINDS array"
assert_nonempty "$PUMP_KINDS" "pump.ts trace switch cases"

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

# --- node-status field parity: Trace.go's nodeStatus JSON struct vs the generated
# --- NodeStatusEvent interface in trace-event-fields.ts. The interface is generated
# --- FROM Trace.go, so this catches a hand-edit of the generated file or a generator
# --- bug — Go's field set and the TS payload's field set must be identical.
TRACE_GO_FILE="$REPO_ROOT/Trace/Trace.go"
TRACE_FIELDS_FILE="$REPO_ROOT/tools/topology-vscode/src/schema/trace-event-fields.ts"

# json tags of the `type nodeStatus struct { ... }` block in Trace.go's MarshalJSON.
fields_from_go() {
  awk '/type nodeStatus struct {/{f=1; next} f&&/}/{f=0} f' "$TRACE_GO_FILE" \
    | grep -o 'json:"[^"]*"' \
    | sed -e 's/json:"//' -e 's/"$//' -e 's/,.*//' \
    | sort
}

# field names of the `export interface NodeStatusEvent { ... }` block.
fields_from_ts() {
  awk '/export interface NodeStatusEvent {/{f=1; next} f&&/^}/{f=0} f' "$TRACE_FIELDS_FILE" \
    | grep -oE '^[[:space:]]*[a-zA-Z0-9_]+' \
    | tr -d ' ' \
    | sort
}

GO_FIELDS=$(fields_from_go)
TS_FIELDS=$(fields_from_ts)
assert_nonempty "$GO_FIELDS" "nodeStatus struct fields (Trace.go)"
assert_nonempty "$TS_FIELDS" "NodeStatusEvent fields (trace-event-fields.ts)"

FIELD_MISSING=$(comm -23 <(echo "$GO_FIELDS") <(echo "$TS_FIELDS"))
FIELD_EXTRA=$(comm -13 <(echo "$GO_FIELDS") <(echo "$TS_FIELDS"))

if [[ -n "$FIELD_MISSING" ]]; then
  echo "trace-kind-parity: node-status fields in Trace.go but missing from NodeStatusEvent:"
  while IFS= read -r fld; do
    echo "  missing field: \"$fld\""
    HITS=$((HITS + 1))
  done <<< "$FIELD_MISSING"
fi
if [[ -n "$FIELD_EXTRA" ]]; then
  echo "trace-kind-parity: fields in NodeStatusEvent not in Trace.go nodeStatus struct:"
  while IFS= read -r fld; do
    echo "  extra field: \"$fld\""
    HITS=$((HITS + 1))
  done <<< "$FIELD_EXTRA"
fi

if [[ $HITS -eq 0 ]]; then
  echo "trace-kind-parity: clean"
  exit 0
fi

echo ""
echo "trace-kind-parity: $HITS divergence(s) found"
exit 1
