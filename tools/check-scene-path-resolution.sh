#!/usr/bin/env bash
set -euo pipefail

# check-scene-path-resolution.sh — guard: path resolution must live in scene_paths.go.
#
# All five scene persisters (camera, fade, overlays, node-pos, anchor) and their loaders
# must resolve topologyPath via sceneTreeRoot / sceneJSONPath (scene_paths.go). A persister
# that hand-rolls os.Stat+IsDir will diverge between the directory-form and the file-form
# topologyPath — the exact bug that recurred three times in one session.
#
# This guard FAILS if os.Stat( + IsDir() appears in any nodes/Wiring/*.go file OUTSIDE
# scene_paths.go, UNLESS the line carries a trailing `// path-resolution-ok:` marker
# (for genuinely-unrelated IsDir uses, e.g. the loader dispatch in loader.go).
#
# Exit 0 when clean.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
WIRING_DIR="$REPO_ROOT/nodes/Wiring"
RESOLVER="$WIRING_DIR/scene_paths.go"

# POSITIVE ASSERTIONS — this guard had none, and was the only one in the suite that could
# pass vacuously. `find` on a missing dir writes to stderr and emits nothing; process-
# substitution failure does NOT trip `set -e`; the while loop then reads zero lines, HITS
# stays 0, and it prints "clean (all IsDir path-resolution lives in scene_paths.go)" — a
# confident all-clear having scanned nothing. Rename nodes/Wiring/ and it congratulates you.
#
# Its siblings already do this (check-ts-computes-no-geometry.sh has both a dir check and a
# "the scan must actually see source files" count); that hardening never propagated here.
if [[ ! -d "$WIRING_DIR" ]]; then
  echo "check-scene-path-resolution: MISCONFIGURED — $WIRING_DIR not found (moved/renamed?)." >&2
  echo "  Refusing to report clean without scanning anything; update WIRING_DIR in $(basename "$0")." >&2
  exit 1
fi

# The file whose authority is the entire point of the guard must exist. Exempting a resolver
# that has been deleted would make every other file trivially compliant.
if [[ ! -f "$RESOLVER" ]]; then
  echo "check-scene-path-resolution: MISCONFIGURED — $RESOLVER not found." >&2
  echo "  This guard exempts scene_paths.go as the authoritative resolver; if it is gone," >&2
  echo "  the invariant it enforces no longer has a home. Update the guard deliberately." >&2
  exit 1
fi

GO_FILE_COUNT=$(find "$WIRING_DIR" -maxdepth 1 -name "*.go" | wc -l | tr -d ' ')
if [[ "$GO_FILE_COUNT" -eq 0 ]]; then
  echo "check-scene-path-resolution: MISCONFIGURED — no .go files found under $WIRING_DIR." >&2
  echo "  The scan must actually see source files; refusing a vacuous pass." >&2
  exit 1
fi

HITS=0
report() {
  printf '%s\n' "$1"
  HITS=$((HITS + 1))
}

# Find all IsDir() calls in Wiring Go files, excluding:
#   - scene_paths.go (the authoritative resolver)
#   - lines with the exemption marker
#   - test files (tests may use os.Stat for fixture checks)
while IFS= read -r file; do
  [[ "$file" == *"_test.go" ]] && continue
  [[ "$(basename "$file")" == "scene_paths.go" ]] && continue

  while IFS= read -r line; do
    # Strip the marker-exempt lines.
    [[ "$line" == *"// path-resolution-ok:"* ]] && continue
    report "hand-rolled-IsDir: $file: $line"
  done < <(grep -n "IsDir()" "$file" 2>/dev/null || true)
done < <(find "$WIRING_DIR" -maxdepth 1 -name "*.go" -not -path "*/node_modules/*")

if [[ $HITS -eq 0 ]]; then
  echo "check-scene-path-resolution: clean ($GO_FILE_COUNT files scanned; all IsDir path-resolution lives in scene_paths.go)"
  exit 0
fi

echo ""
echo "check-scene-path-resolution: $HITS hit(s) — resolve topologyPath via sceneTreeRoot/sceneJSONPath in scene_paths.go, not hand-rolled IsDir. Mark unrelated uses with '// path-resolution-ok:'"
exit 1
