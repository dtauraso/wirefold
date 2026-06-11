#!/usr/bin/env bash
set -euo pipefail

# Verifies that pump.ts contains no polling or firing logic: no setInterval,
# setTimeout, or bare while-loop.
# Exit 0 if clean; exit 1 with file:line reports on any hits.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

PUMP_FILE="$REPO_ROOT/tools/topology-vscode/src/webview/three/pump.ts"

# Fail loudly if the pump moved — a missing PUMP_FILE would make every scan below
# trivially "clean", silently disabling the guard.
if [[ ! -f "$PUMP_FILE" ]]; then
  echo "no-ts-timers: pump.ts not found at $PUMP_FILE — update PUMP_FILE in this guard" >&2
  exit 1
fi

HITS=0
scan() {
  local pattern="$1"
  local hit
  while IFS= read -r hit; do
    [[ -z "$hit" ]] && continue
    printf '%s: pattern "%s"\n' "$hit" "$pattern"
    HITS=$((HITS + 1))
  done < <(grep -n "$pattern" "$PUMP_FILE" 2>/dev/null || true)
}

scan "setInterval"
scan "setTimeout"
scan "while ("

if [[ $HITS -eq 0 ]]; then
  echo "no-ts-timers: clean"
  exit 0
fi

echo ""
echo "no-ts-timers: $HITS hit(s) found — polling/firing logic must not live in pump.ts"
exit 1
