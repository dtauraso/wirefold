#!/usr/bin/env bash
set -euo pipefail

# check-ts-computes-no-geometry.sh — Phase 2 + Phase 3 guard.
#
# Asserts the TS webview computes NO geometry at all: Go owns the clock, computes
# every bead position AND every edge curve, and streams both; TS plots/draws only
# (MODEL.md). This guard fails if the deleted math returns:
#
#   - PulseBead's per-frame curve sampling: getPointAt        (scene-content.tsx)
#   - per-bead arc-length for position:     rfArcLength        (geometry-helpers.ts)
#   - per-bead travel-time re-derive:       arcLengthToSimLatencyMs
#   - moveNode in-flight bead progress patch: patchPulse
#   - the edge-curve (wire-tube shape) builders, Phase 3:
#       buildPortCurve / buildEdgeCurve     (geometry-helpers.ts)
#
# Scope: the webview source tree (excludes node_modules / out / generated). Each
# forbidden token is reported with file:line if found. Exit 0 when clean.
#
# Note on what is still allowed: portWorldPos / portDir / nodeWorldPos / nodeRadius
# place the node + PORT SPHERES (and project labels). They ALSO source the straight
# wire's endpoints and the pulse bead's placement — both from the editor-owned LIVE
# local node positions, so a dragged node carries its wire + bead with no Go round-trip
# (MODEL.md: Go owns the bead's PROGRESS/fraction t via the clock; the editor owns live
# node placement during interaction). Placing Go's fraction t (pulse.frac) at
# lerp(localStart, localEnd, t) is NOT geometry computation — it is placement of a
# Go-owned value on editor-owned node positions, the same exception nodeWorldPos uses.
# The straight segment Start/End come from portWorldPos (node center + port dir ×
# radius); the wire SHAPE is a straight LineCurve3, not a TS-computed curve.

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
  "buildPortCurve"
  "buildEdgeCurve"
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
