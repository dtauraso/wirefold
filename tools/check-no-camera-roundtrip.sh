#!/usr/bin/env bash
# check-no-camera-roundtrip.sh — guard that the camera stays angle-of-record.
#
# The round-trip (store a Cartesian position, reconstruct the angles from it each tick)
# is what creates the pole singularity and the makeSafe clamp. We forbid its fingerprints
# in the webview's 3D nav/camera code. The legitimate Cartesian edges are:
#   - polar.ts        : deltaToPolar (input edge) + fromWorld (cursor pick) — allowed
#   - PanPolarOverlay  : screen-space overlay math — allowed
# Everything else that reconstructs camera state from a position is banned.
#
# Mirrors tools/check-no-await-on-bridge.sh. Exit 1 on any hit.

set -euo pipefail

DIR="tools/topology-vscode/src/webview/three"
# Files allowed to use the input/pick edges.
EXCLUDE='polar\.ts|PanPolarOverlay\.tsx'

# Banned symbols: camera state reconstructed from a Cartesian position.
PATTERN='setFromVector3|setFromCartesianCoords|\.makeSafe\(|new THREE\.Spherical'

hits=$(grep -rnE "$PATTERN" "$DIR" --include='*.ts' --include='*.tsx' 2>/dev/null \
       | grep -vE "$EXCLUDE" || true)

if [ -n "$hits" ]; then
  echo "✗ camera round-trip fingerprint(s) found — the camera must stay angle-of-record:"
  echo "$hits"
  echo "  (reconstructing angles from a position reintroduces the pole singularity / makeSafe.)"
  exit 1
fi

echo "✓ no camera round-trip: camera state is not reconstructed from a Cartesian position."
