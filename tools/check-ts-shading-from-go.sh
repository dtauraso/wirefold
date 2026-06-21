#!/usr/bin/env bash
set -euo pipefail

# check-ts-shading-from-go.sh — Phase 4 guard.
#
# Asserts the TS webview sources its base SHADING PARAMETERS from Go, not from
# hardcoded constants. Go owns the shading parameter values (glass/material,
# environment/lighting, wire-tube + bead appearance); they are generated into
# src/schema/shading-params.ts from nodes/Wiring/shading_params.go. TS keeps only
# the GPU machinery (creating THREE materials, baking the PMREM env map, binding)
# — no shading VALUES of its own (MODEL.md; docs/go-authoritative-clock "tsgo").
#
# This is the shading analogue of check-ts-computes-no-geometry.sh: it fences the
# specific literals that were relocated to Go out of scene-content.tsx, and fails
# if any of them returns. Exit 0 when clean.
#
# What this guard intentionally does NOT fence (these stay in TS — they are not
# base scene shading params):
#   - selection/hover highlight colors (#ffcc00 / #aaddff) and the selection halo
#     (#ff5a00): interaction-state UI affordances.
#   - init-pulse bead colors (#ffffff/#000000/#888888 in INIT_PULSE_COMPONENTS):
#     a per-node DATA visualization (value→appearance), not scene shading.
#   - node.data.fill / node.data.stroke fallbacks: those values are already
#     Go-authoritative (NODE_DEFS, generated from each SPEC.md ## View).
#   - emissive 0x000000 / emissiveIntensity 0: the "no emissive" neutral.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Scan the whole three/ render dir, not a single file: scene-content.tsx was split
# into cohesive modules (scene-graph.tsx glass/wire material, scene-env.tsx env map
# + lights, scene-beads.tsx beads), so the shading code now lives across several
# files. Scanning the dir keeps the guard robust to future relocations.
SCAN_DIR="$REPO_ROOT/tools/topology-vscode/src/webview/three"

if [[ ! -d "$SCAN_DIR" ]]; then
  echo "ts-shading-from-go: render dir not found at $SCAN_DIR" >&2
  exit 1
fi

# Forbidden patterns — the exact literals relocated to nodes/Wiring/shading_params.go.
# Each is the hardcoded value that previously fed a GPU material/light/env; its
# reappearance in scene-content.tsx means a shading value crept back into TS.
# Patterns are extended-regex, anchored to the prop/arg context where possible to
# avoid matching unrelated coordinates.
FORBIDDEN=(
  # Node-body glass material props (MeshPhysicalMaterial).
  'transmission=\{1'
  'roughness=\{0\.12\}'
  'ior=\{1\.5\}'
  'clearcoatRoughness=\{0\.1\}'
  'envMapIntensity=\{1'
  'opacity=\{faded \? fadeOpacity \* 0\.6 : 0\.92\}'
  # Procedural env map: sky radius, vertex-tint literals, light colors/intensities, PMREM blur.
  'SphereGeometry\(50,'
  '0\.78 \+ \(1\.0 - 0\.78\)'
  '0\.77 \+ \(0\.88 - 0\.77\)'
  '0\.74 \+ \(0\.75 - 0\.74\)'
  'AmbientLight\(0xffffff, 0\.9\)'
  'DirectionalLight\(0xffeedd, 0\.45\)'
  'DirectionalLight\(0xaabbff, 0\.3\)'
  'fromScene\(envScene, 0\.04\)'
  # Scene lights.
  'ambientLight intensity=\{0\.6\}'
  'directionalLight position=\{\[0, 0, 10\]\} intensity=\{0\.8\}'
  # Wire-tube material.
  'color="#5599cc"'
  '0x2255aa'
  'emissiveIntensity=\{0\.8\}'
  # In-flight bead material.
  'emissiveIntensity=\{2\.5\}'
)

HITS=0
for pat in "${FORBIDDEN[@]}"; do
  while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    printf '%s  (forbidden shading literal: %s)\n' "$line" "$pat"
    HITS=$((HITS + 1))
  done < <(grep -rnE --include='*.ts' --include='*.tsx' "$pat" "$SCAN_DIR" 2>/dev/null || true)
done

# Positive assertion: the render dir must import the Go-supplied shading params.
# If the import is gone, TS is no longer sourcing shading from Go even if no
# forbidden literal is present (e.g. someone inlined a different value).
if ! grep -rq --include='*.ts' --include='*.tsx' 'from "../../schema/shading-params"' "$SCAN_DIR"; then
  echo 'ts-shading-from-go: three/ does not import from "../../schema/shading-params" — shading params must come from Go'
  HITS=$((HITS + 1))
fi
if ! grep -rq --include='*.ts' --include='*.tsx' 'SHADING_PARAM_NODE_TRANSMISSION' "$SCAN_DIR"; then
  echo 'ts-shading-from-go: SHADING_PARAM_NODE_TRANSMISSION not used — node glass material is not reading Go params'
  HITS=$((HITS + 1))
fi

if [[ $HITS -eq 0 ]]; then
  echo "ts-shading-from-go: clean (TS binds Go-supplied shading params; no relocated shading literals in three/)"
  exit 0
fi

echo ""
echo "ts-shading-from-go: $HITS hit(s) — shading parameter VALUES must live in Go (nodes/Wiring/shading_params.go), not TS"
exit 1
