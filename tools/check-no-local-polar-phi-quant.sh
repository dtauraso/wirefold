#!/usr/bin/env bash
# check-no-local-polar-phi-quant.sh — guard against reintroducing the +y POLE
# SINGULARITY into the local-polar lock cascade.
#
# A node's local-polar offset to a doubly-linked neighbor stores its DIRECTION as an
# EXACT unit vector (layout_holder.go LocalPolar.Dir), NOT as a quantized azimuth about
# the fixed +y pole. The retired representation did `cart2polar(neighbor-owner)` then
# `Round(pol.Phi / stepPhi)`; near +y one phi-cell spans r*sinTheta*stepPhi -> 0, so a
# fixed world nudge crossed unbounded phi-cells and the persisted direction became
# garbage. That decomposition was removed end-to-end (commit "store local-polar offset
# direction as exact unit vector").
#
# This guard fails if ANY `Round(... .Phi ...)` reappears in the local-polar cascade
# files. It does NOT touch quantized_layout.go: the SCENE-triple's iPhi
# (measureScalar) is a legitimate, HARMLESS quantization — the persisted position is the
# EXACT scenePolar (lossless), so the scene-triple's phi cell never reconstructs a
# position (proven by pole_drag_probe_test.go: near-pole drag reload drift ~1e-10). The
# two concerns are cleanly separated BY FILE: cascade phi-quantization is the bug;
# scene-triple phi-quantization lives only in quantized_layout.go.
#
# Exit 1 on any hit or on misconfiguration (a listed file missing → the cascade moved
# and this guard would otherwise report a false clean).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# The local-polar cascade lives in these files (layout_holder.go owns the struct;
# node_move.go runs the drag/equalize write sites; loader.go computes the fresh list).
FILES=(
  "$REPO_ROOT/nodes/Wiring/layout_holder.go"
  "$REPO_ROOT/nodes/Wiring/node_move.go"
  "$REPO_ROOT/nodes/Wiring/loader.go"
)

missing=0
for f in "${FILES[@]}"; do
  if [ ! -f "$f" ]; then
    echo "✗ no-local-polar-phi-quant: MISCONFIGURED — missing $f" >&2
    echo "  (the local-polar cascade moved/renamed? update FILES in $(basename "$0"))" >&2
    missing=1
  fi
done
[ "$missing" -eq 0 ] || exit 1

# Match `Round(` ... `.Phi` on one line — the pole-singular quantize-azimuth pattern.
if hits=$(grep -nE 'Round\([^)]*\.Phi' "${FILES[@]}" 2>/dev/null); then
  echo "✗ no-local-polar-phi-quant: pole-singular azimuth quantization reintroduced" >&2
  echo "  The local-polar offset DIRECTION must be a stored exact unit vector" >&2
  echo "  (LocalPolar.Dir), never Round(cart2polar(...).Phi / step) about the +y pole." >&2
  echo "$hits" | sed 's/^/    /' >&2
  exit 1
fi

echo "✓ no-local-polar-phi-quant: local-polar cascade quantizes no azimuth about +y"
