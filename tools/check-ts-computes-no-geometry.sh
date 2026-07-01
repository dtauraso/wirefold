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
# place the node + PORT SPHERES (and project labels), not the wire curve or a bead
# position. nodeWorldPos reads Go's emitted node-geometry center (Go re-emits it on
# node-move); the wire tube reads Go's streamed straight-segment endpoints
# (edge-geometry store); the bead reads Go's streamed position (pulse.pos). TS sources
# none of the wire/bead geometry from local React Flow node positions.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

SRC_DIR="$REPO_ROOT/tools/topology-vscode/src"

# Fail loud if the scanned tree moved — a silently-missing dir would make every
# grep below error into `|| true`, keeping HITS at 0 and reporting a false clean
# (mirror check-ts-shading-from-go.sh / check-slot-phase-boundary.sh).
if [[ ! -d "$SRC_DIR" ]]; then
  echo "ts-computes-no-geometry: MISCONFIGURED — scan dir not found: $SRC_DIR" >&2
  exit 1
fi

# Positive sanity assertion: the scan must actually see source files. If the tree
# is present but contains no .ts/.tsx (moved/renamed layout), a zero-hit result is
# vacuous, not clean.
ts_file_count=$(find "$SRC_DIR" \( -name "*.ts" -o -name "*.tsx" \) | head -1 | wc -l | tr -d ' ')
if [[ "$ts_file_count" -eq 0 ]]; then
  echo "ts-computes-no-geometry: MISCONFIGURED — no .ts/.tsx files under $SRC_DIR" >&2
  exit 1
fi

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
  done < <(grep -arn --include="*.ts" --include="*.tsx" "$token" "$SRC_DIR" 2>/dev/null || true)
done

if [[ $HITS -eq 0 ]]; then
  echo "ts-computes-no-geometry: clean (TS plots Go's position stream; computes no bead geometry)"
  exit 0
fi

echo ""
echo "ts-computes-no-geometry: $HITS hit(s) — bead position/geometry math must live in Go, not TS"
exit 1
