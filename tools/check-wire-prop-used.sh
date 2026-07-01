#!/usr/bin/env bash
set -euo pipefail

# check-wire-prop-used.sh
# ------------------------------------------------------------------------------
# CLAUDE.md's Primitive landing rule says a new wire prop "must be added to
# WIRE_PROPS in wire-defs.ts AND threaded through SingleEdgeTube in the same
# commit it is used." check-generated only proves the prop REACHES the generated
# wire-defs.ts — a prop can be generated yet never rendered. This guard closes
# that gap: every key in WIRE_PROPS must be REFERENCED somewhere in the
# edge-render path, or it is a silently-dead wire prop.
#
# Exit 0 if every (non-allowlisted) WIRE_PROPS key is referenced; exit 1 with a
# report naming any key that is generated but never consumed by the renderer.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

WIRE_DEFS_FILE="$REPO_ROOT/tools/topology-vscode/src/schema/wire-defs.ts"

# The edge-render path: files where a wire prop legitimately flows from EdgeData /
# WireProps into the drawn tube (geometry, styling, beads). A prop referenced in
# ANY of these counts as rendered.
RENDER_PATH_FILES=(
  "$REPO_ROOT/tools/topology-vscode/src/webview/three/scene-graph.tsx"
  "$REPO_ROOT/tools/topology-vscode/src/webview/three/ThreeView.tsx"
  "$REPO_ROOT/tools/topology-vscode/src/webview/three/scene-beads.tsx"
  "$REPO_ROOT/tools/topology-vscode/src/webview/three/edge-geometry.ts"
  "$REPO_ROOT/tools/topology-vscode/src/webview/three/edge-creation.ts"
  "$REPO_ROOT/tools/topology-vscode/src/webview/state/adapter/spec-to-flow-helpers.ts"
)

# Allowlist: WIRE_PROPS keys that are legitimately NOT rendered-tube props.
#   label — edge identity (used for save/load round-trip and node/edge naming),
#           not a geometry/style prop of the drawn tube. Exempt from the
#           render-path requirement.
ALLOWLIST=" label "

for f in "$WIRE_DEFS_FILE" "${RENDER_PATH_FILES[@]}"; do
  if [[ ! -f "$f" ]]; then
    echo "wire-prop-used: MISCONFIGURED — file not found: $f" >&2
    exit 1
  fi
done

assert_nonempty() { # value label
  if [[ -z "$(printf '%s' "$1" | tr -d '[:space:]')" ]]; then
    echo "wire-prop-used: EMPTY extracted set for '$2' — WIRE_PROPS block missing or renamed; refusing vacuous pass" >&2
    exit 1
  fi
}

# Keys inside the `export const WIRE_PROPS: Record<...> = { ... };` object.
# -a: force text mode (portable). [[:space:]]: POSIX class in place of GNU-only \s.
keys_from_wire_defs() {
  awk '/export const WIRE_PROPS/{f=1; next} f&&/^};/{f=0} f' "$WIRE_DEFS_FILE" \
    | grep -aoE '^[[:space:]]*[a-zA-Z0-9_]+:' \
    | sed -E 's/[[:space:]]//g; s/://' \
    | sort -u
}

WIRE_KEYS=$(keys_from_wire_defs)
assert_nonempty "$WIRE_KEYS" "WIRE_PROPS keys"

HITS=0
while IFS= read -r key; do
  [[ -z "$key" ]] && continue
  # Allowlisted keys are exempt from the render-path requirement.
  case "$ALLOWLIST" in
    *" $key "*) continue ;;
  esac
  # A prop is "referenced" if it appears as a property access `.<key>` anywhere
  # in the render path. -a text mode; word boundary via a non-identifier lookahead
  # emulated by matching `.<key>` not followed by an identifier char.
  if ! grep -aE "\.${key}([^A-Za-z0-9_]|\$)" "${RENDER_PATH_FILES[@]}" >/dev/null 2>&1; then
    echo "wire-prop-used: WIRE_PROPS key \"$key\" is generated but never referenced in the edge-render path:"
    for f in "${RENDER_PATH_FILES[@]}"; do
      echo "    (checked) ${f#$REPO_ROOT/}"
    done
    HITS=$((HITS + 1))
  fi
done <<< "$WIRE_KEYS"

if [[ $HITS -eq 0 ]]; then
  echo "wire-prop-used: clean"
  exit 0
fi

echo ""
echo "wire-prop-used: $HITS unrendered wire prop(s) — thread each through SingleEdgeTube/DoubleEdgeOverlay (or allowlist it in this guard if it is not a tube prop)"
exit 1
