#!/usr/bin/env bash
set -euo pipefail

# check-ts-computes-no-geometry.sh — Phase 2 guard.
#
# Asserts the TS webview computes NO bead position/geometry: Go owns the clock,
# computes every bead position, and streams it; TS plots only (MODEL.md). This
# guard fails if the deleted position math returns:
#
#   - PulseBead's per-frame curve sampling: getPointAt        (scene-content.tsx)
#   - per-bead arc-length for position:     rfArcLength        (geometry-helpers.ts)
#   - per-bead travel-time re-derive:       arcLengthToSimLatencyMs
#   - moveNode in-flight bead progress patch: patchPulse
#
# Scope: the webview source tree (excludes node_modules / out / generated). Each
# forbidden token is reported with file:line if found. Exit 0 when clean.
#
# Note on what is INTENTIONALLY allowed: buildPortCurve / buildEdgeCurve build the
# drawn WIRE-TUBE geometry (the wire's shape), not a bead position, so they are not
# matched here. The bead reads its position from Go's stream (pulse.pos).

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

SRC_DIR="$REPO_ROOT/tools/topology-vscode/src"

# Forbidden tokens — any occurrence (code or comment) is a regression signal. The
# Phase-2 deletion removed these names entirely, so a clean tree has zero hits and
# their reappearance means bead-position math crept back into TS.
FORBIDDEN=(
  "getPointAt"
  "rfArcLength"
  "arcLengthToSimLatencyMs"
  "patchPulse"
)

HITS=0
for token in "${FORBIDDEN[@]}"; do
  while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    printf '%s  (forbidden: %s)\n' "$line" "$token"
    HITS=$((HITS + 1))
  done < <(grep -rn --include="*.ts" --include="*.tsx" "$token" "$SRC_DIR" 2>/dev/null || true)
done

if [[ $HITS -eq 0 ]]; then
  echo "ts-computes-no-geometry: clean (TS plots Go's position stream; computes no bead geometry)"
  exit 0
fi

echo ""
echo "ts-computes-no-geometry: $HITS hit(s) — bead position/geometry math must live in Go, not TS"
exit 1
