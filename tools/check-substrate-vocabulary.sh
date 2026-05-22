#!/usr/bin/env bash
set -euo pipefail

# Scans the Go substrate for banned vocabulary that signals model drift.
# Exit 0 if clean; exit 1 with file:line:token reports on any hits.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Substrate source files. Excludes Trace/Trace.go (legitimate use of Step as
# the event ordinal field) and this script.
substrate_files() {
  find "$REPO_ROOT/nodes" -name "*.go" -not -path "*/Trace/*"
  [[ -f "$REPO_ROOT/Wire.go" ]] && echo "$REPO_ROOT/Wire.go"
  [[ -f "$REPO_ROOT/main.go" ]] && echo "$REPO_ROOT/main.go"
}

HITS=0
scan() {
  local token="$1"
  local hit
  while IFS= read -r hit; do
    [[ -z "$hit" ]] && continue
    printf '%s: token "%s"\n' "$hit" "$token"
    HITS=$((HITS + 1))
  done < <(substrate_files | sort -u | xargs grep -n -w "$token" 2>/dev/null || true)
}

for t in tick round schedule ack latch cohort scheduler deadline step; do
  scan "$t"
done

if [[ $HITS -eq 0 ]]; then
  echo "substrate-vocabulary: clean"
  exit 0
fi

echo ""
echo "substrate-vocabulary: $HITS hit(s) found"
exit 1
