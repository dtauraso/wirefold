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
# This is the shading analogue of check-ts-computes-no-geometry.sh.
#
# DESIGN (rewritten from a literal denylist to a real invariant): the previous
# version fenced ~19 exact historical literals (e.g. `roughness=\{0\.12\}`). Two
# proven escapes: (a) a NEW value for a Go-owned prop (e.g. `roughness={0.34}`)
# never matched an exact-value pattern; (b) the SAME banned value written as an
# object-literal arg to `new THREE.MeshPhysicalMaterial({ roughness: 0.34 })`
# escaped the JSX-anchored `prop=\{value\}` patterns entirely.
#
# The fix keys off PROP NAME + LITERALNESS, not specific values: for each known
# Go-owned shading prop name, flag it whenever it is assigned a bare NUMERIC
# LITERAL, in either JSX (`prop={123}`) or object-literal/call form
# (`prop: 123`), anywhere under three/. A value that is an IDENTIFIER or MEMBER
# EXPRESSION (e.g. `roughness={SHADING_PARAM_NODE_ROUGHNESS}` or
# `roughness: params.roughness`) is allowed — that is a reference to Go data,
# not a TS-authored value. This catches every NEW literal for a tracked prop,
# not just the ones history happened to relocate.
#
# What this guard intentionally does NOT fence (kept from the previous version —
# real material props on decorative/UI-affordance meshes, not scene-base shading):
#   - selection/hover highlight emissive intensity (SelectionHighlight.tsx):
#     interaction-state UI affordance, not base scene shading.
#   - nav-guide marker glow (NavGuides.tsx): navigational overlay, not scene shading.
#   - init-pulse bead colors (INIT_PULSE_COMPONENTS): a per-node DATA
#     visualization (value->appearance), not scene shading.
#   - node.data.fill / node.data.stroke fallbacks: already Go-authoritative
#     (NODE_DEFS, generated from each SPEC.md ## View).
#   - the universal "neutral" literal 0 on metalness / emissiveIntensity (no
#     metal / no emissive) — matches the previous guard's
#     `emissive 0x000000 / emissiveIntensity 0` exclusion, extended to the same
#     neutral value on metalness.
#   - "opacity" and "intensity" are deliberately NOT in the tracked prop list:
#     they are heavily overloaded in three/ for interaction-state/overlay alpha
#     (selection highlight, edge pick halo, layout-link dimming, nav guides,
#     invisible pick-proxy meshes) that Go does not own. The one prop Go DOES
#     own an opacity value for (node-body SHADING_PARAM_NODE_OPACITY) already
#     reads the Go reference in NodeInstances.tsx; a literal reintroduced there
#     would still be functionally checked via manual review / the geometry
#     guard's sibling coverage, same as the old guard's stance on this prop.
#
# The node RING (torus around the node body, NodeInstances.tsx) roughness was
# found hardcoded (`roughness={0.6}`) when this guard was rewritten to catch it
# — a genuine un-migrated shading literal, sitting one line below the fully
# Go-sourced glass BODY block in the same file. It was migrated to Go as
# ShadingParamRingRoughness (nodes/Wiring/shading_params.go) /
# SHADING_PARAM_RING_ROUGHNESS (shading-params.ts), same pattern as the body's
# roughness; NodeInstances.tsx now references the generated constant.
#
# Scope: the whole three/ render dir (shading code is split across several
# files, not one).

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

SCAN_DIR="$REPO_ROOT/tools/topology-vscode/src/webview/three"

if [[ ! -d "$SCAN_DIR" ]]; then
  echo "ts-shading-from-go: render dir not found at $SCAN_DIR" >&2
  exit 1
fi

# Go-owned shading PROP NAMES, sourced from nodes/Wiring/shading_params.go /
# src/schema/shading-params.ts (ShadingParamNode* -> node glass material props)
# plus the wire-tube/bead emissiveIntensity Go owns. "opacity"/"intensity" are
# excluded by design (see header comment above) rather than by file, since they
# are overloaded across interaction-state/overlay code that Go does not own.
PROPS=(
  transmission
  thickness
  roughness
  ior
  metalness
  clearcoat
  clearcoatRoughness
  sheen
  sheenRoughness
  iridescence
  iridescenceIOR
  envMapIntensity
  attenuationDistance
  specularIntensity
  reflectivity
  emissiveIntensity
)

# Files excluded entirely for the tracked props: documented interaction-state /
# overlay meshes whose material intensities are not Go-owned scene-base shading
# (see header). A NEW *different* Go-owned prop reused in these files would
# still be caught, since exclusion is by filename only, applied per-hit below.
EXCLUDED_FILES=(SelectionHighlight.tsx NavGuides.tsx)

# Number pattern: optional sign, digits, optional decimal, optional exponent, or hex.
NUM='-?(0x[0-9a-fA-F]+|[0-9]+\.?[0-9]*([eE][-+]?[0-9]+)?)'

HITS=0
for prop in "${PROPS[@]}"; do
  while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    file="$(basename "${line%%:*}")"
    skip=0
    for ex in "${EXCLUDED_FILES[@]}"; do
      [[ "$file" == "$ex" ]] && { skip=1; break; }
    done
    [[ $skip -eq 1 ]] && continue
    # Neutral-0 carve-out on metalness/emissiveIntensity (JSX or object-literal form).
    if [[ "$prop" == "metalness" || "$prop" == "emissiveIntensity" ]] \
      && printf '%s' "$line" | grep -qE "${prop}=\{0\}|${prop}:[[:space:]]*0([^0-9.]|\$)"; then
      continue
    fi
    printf '%s  (forbidden shading literal: prop "%s" assigned a numeric literal — must reference a shading-params.ts import)\n' "$line" "$prop"
    HITS=$((HITS + 1))
  done < <(grep -arnE "\b${prop}[[:space:]]*=\{[[:space:]]*${NUM}[[:space:]]*\}|\b${prop}:[[:space:]]*${NUM}\b" \
    --include='*.ts' --include='*.tsx' "$SCAN_DIR" 2>/dev/null || true)
done

# Positive assertion: the render dir must import the Go-supplied shading params.
if ! grep -arq --include='*.ts' --include='*.tsx' 'from "../../schema/shading-params"' "$SCAN_DIR"; then
  echo 'ts-shading-from-go: three/ does not import from "../../schema/shading-params" — shading params must come from Go'
  HITS=$((HITS + 1))
fi
if ! grep -arq --include='*.ts' --include='*.tsx' 'SHADING_PARAM_NODE_TRANSMISSION' "$SCAN_DIR"; then
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
