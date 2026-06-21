#!/usr/bin/env bash
# check-polar-only-nav.sh — guard that the nav handler stays polar-only.
#
# The rotation handler in interaction-controls.ts must do NO Cartesian math itself.
# All sphere/angle math (cross products, axis construction, setFromUnitVectors) must
# live inside polar.ts — the sole quarantine. The handler may read camera state and
# write it, and may call polar.ts helpers, but must not build rotation axes or do
# cross products directly.
#
# Allowed exception: `.project(` used for the one-time sphere-center projection at
# pointer-down is marked with a trailing `// polar-center-projection` comment and
# excluded from the check.
#
# Mirrors tools/check-no-camera-roundtrip.sh. Exit 1 on any hit.

set -euo pipefail

# Only check the nav handler file(s) — NOT polar.ts (it is the quarantine).
# The hook lives in interaction-controls.ts; the handler bodies live in
# interaction-handlers.ts. Both must stay polar-only.
NAV_FILES=(
  "tools/topology-vscode/src/webview/three/interaction-controls.ts"
  "tools/topology-vscode/src/webview/three/interaction-handlers.ts"
)

# Banned symbols: Cartesian rotation/axis math that belongs in polar.ts.
PATTERN='setFromUnitVectors|\.cross\(|new THREE\.Raycaster|\.unproject\(|setFromAxisAngle|setFromMatrixColumn|new THREE\.Spherical'

# Lines marked `// polar-nav-ok` are exempted (node-drag/pan — not in the rotation path).
hits=$(grep -nE "$PATTERN" "${NAV_FILES[@]}" 2>/dev/null | grep -v 'polar-nav-ok' || true)

if [ -n "$hits" ]; then
  echo "✗ polar-nav violation(s) found — all rotation/axis math must live in polar.ts:"
  echo "$hits"
  echo "  (banned: setFromUnitVectors, .cross(, new THREE.Raycaster, .unproject(, setFromAxisAngle, setFromMatrixColumn, new THREE.Spherical)"
  exit 1
fi

echo "✓ polar-only nav: no banned Cartesian rotation math in ${NAV_FILES[*]}."
