#!/usr/bin/env bash
set -euo pipefail

# Verifies that slot-phase transition logic (writing { phase: "filled" } or
# { phase: "empty" } into a SlotEntry) does not appear outside its canonical
# homes:
#   TS: tools/topology-vscode/src/webview/three/pump.ts
#   Go: nodes/Wiring/paced_wire.go  (uses hasSend bool; no string phase literals)
#
# Read-only render checks (.phase === "filled") and type-definition files
# (messages.ts) are NOT transition logic and are not flagged.
#
# Exit 0 if clean; exit 1 with file:line reports on any hits.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

RF_DIR="$REPO_ROOT/tools/topology-vscode/src/webview/three"
PUMP_FILE="$RF_DIR/pump.ts"

# Fail loud if the scanned tree moved — a silently-missing dir would make this
# guard a no-op (the exact failure that left it dead when rendering moved
# rf/ -> three/). Never let the scan target vanish unnoticed.
if [[ ! -d "$RF_DIR" ]]; then
  echo "slot-phase-boundary: MISCONFIGURED — scan dir not found: $RF_DIR" >&2
  echo "  (renderer files moved? update RF_DIR in $(basename "${BASH_SOURCE[0]}"))" >&2
  exit 1
fi
if [[ ! -f "$PUMP_FILE" ]]; then
  echo "slot-phase-boundary: MISCONFIGURED — canonical home not found: $PUMP_FILE" >&2
  exit 1
fi

HITS=0

# TS scan — transition pattern is: phase: "filled" or phase: "empty" as an
# object property value (comma-separated, runtime form). Exclude pump.ts (the
# canonical home). The type-definition form uses a semicolon: { phase: "filled"; … }
# so limiting the match to the comma form avoids flagging messages.ts.
# We scan the three/ renderer subtree (pump.ts is the canonical home).
ts_scan() {
  local pattern="$1"
  local hit
  while IFS= read -r hit; do
    [[ -z "$hit" ]] && continue
    printf '%s: pattern "%s"\n' "$hit" "$pattern"
    HITS=$((HITS + 1))
  done < <(
    find "$RF_DIR" -name "*.ts" -o -name "*.tsx" | sort |
    grep -v "$(printf '%s' "$PUMP_FILE")" |
    xargs grep -n "$pattern" 2>/dev/null || true
  )
}

# Go scan — slot-phase string literals should never appear in Go files.
# paced_wire.go uses hasSend bool and has no phase string literals at all,
# so the Go scan is unconditional (no file to exempt).
go_files() {
  find "$REPO_ROOT/nodes" -name "*.go"
  [[ -f "$REPO_ROOT/Wire.go" ]] && echo "$REPO_ROOT/Wire.go"
  [[ -f "$REPO_ROOT/main.go" ]] && echo "$REPO_ROOT/main.go"
}

go_scan() {
  local pattern="$1"
  local hit
  while IFS= read -r hit; do
    [[ -z "$hit" ]] && continue
    printf '%s: pattern "%s"\n' "$hit" "$pattern"
    HITS=$((HITS + 1))
  done < <(go_files | sort -u | xargs grep -n "$pattern" 2>/dev/null || true)
}

# Transition pattern: object-literal slot-phase writes (comma form, not type defs).
ts_scan 'phase: "filled",'
ts_scan 'phase: "empty"'

# Go should have no slot-phase string literals at all.
go_scan '"filled"'
go_scan '"empty"'

if [[ $HITS -eq 0 ]]; then
  echo "slot-phase-boundary: clean"
  exit 0
fi

echo ""
echo "slot-phase-boundary: $HITS hit(s) found — slot-phase transition logic must live in pump.ts (TS) or paced_wire.go (Go)"
exit 1
