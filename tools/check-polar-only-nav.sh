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
# interaction-handlers.ts. ALL interaction-*.ts files must stay polar-only —
# glob the family so a future split (e.g. interaction-gestures.ts) is covered
# automatically instead of silently escaping the guard.
NAV_DIR="tools/topology-vscode/src/webview/three"
# Nullglob so a no-match expands to empty (caught by the count check below)
# rather than leaving the literal pattern in the array.
shopt -s nullglob
NAV_FILES=( "$NAV_DIR"/interaction-*.ts )
shopt -u nullglob

if [ ${#NAV_FILES[@]} -eq 0 ]; then
  echo "✗ polar-only nav: MISCONFIGURED — no interaction-*.ts files under $NAV_DIR" >&2
  echo "  (nav handlers moved/renamed? update NAV_DIR in $(basename "$0"))" >&2
  exit 1
fi

# Banned symbols: Cartesian rotation/axis math that belongs in polar.ts.
PATTERN='setFromUnitVectors|\.cross\(|new THREE\.Raycaster|\.unproject\(|setFromAxisAngle|setFromMatrixColumn|new THREE\.Spherical'

# Lines marked `// polar-nav-ok` are exempted (node-drag/pan — not in the rotation path).
hits=$(grep -anE "$PATTERN" "${NAV_FILES[@]}" 2>/dev/null | grep -v 'polar-nav-ok' || true)

if [ -n "$hits" ]; then
  echo "✗ polar-nav violation(s) found — all rotation/axis math must live in polar.ts:"
  echo "$hits"
  echo "  (banned: setFromUnitVectors, .cross(, new THREE.Raycaster, .unproject(, setFromAxisAngle, setFromMatrixColumn, new THREE.Spherical)"
  exit 1
fi

echo "✓ polar-only nav: no banned Cartesian rotation math in ${NAV_FILES[*]}."
