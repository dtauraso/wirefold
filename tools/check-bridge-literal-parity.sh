#!/usr/bin/env bash
set -euo pipefail

# Guards hand-duplicated string literals that cross the TS<->Go boundary and
# have no generator linking them. check-message-kind-parity.sh covers top-level
# msg.Type and check-edit-op-parity.sh covers msg.Op, but two literal axes are
# below both:
#
#   1. viewpoint sub-kinds — the nested vp.Kind discriminator inside
#      op:"viewpoint" (set/orbit/orbit-locked/zoom/pan). Declared in the
#      messages.ts viewpoint union, switched on in stdin_reader.go. Adding a
#      sub-kind on one side silently no-ops on the other.
#   2. the "spec" startup line kind — Go emits {"kind":"spec",...} on startup
#      (loader.go) and TS recognizes it (runCommand.ts). Rename either side and
#      spec-load silently breaks (blank editor).
#
# Exit 0 if both axes are in parity; exit 1 with a report otherwise.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

STDIN_READER="$REPO_ROOT/nodes/Wiring/stdin_reader.go"
MESSAGES_TS="$REPO_ROOT/tools/topology-vscode/src/messages.ts"
LOADER_GO="$REPO_ROOT/nodes/Wiring/loader.go"
RUN_COMMAND_TS="$REPO_ROOT/tools/topology-vscode/src/runCommand.ts"

for f in "$STDIN_READER" "$MESSAGES_TS" "$LOADER_GO" "$RUN_COMMAND_TS"; do
  if [[ ! -f "$f" ]]; then
    echo "bridge-literal-parity: MISCONFIGURED — file not found: $f" >&2
    exit 1
  fi
done

HITS=0

# --- Axis 1: viewpoint sub-kinds --------------------------------------------
# Go: `case "..."` lines inside the `switch vp.Kind { ... }` block, bounded by the
# VP_KINDS_START / VP_KINDS_END sentinel comments around it in stdin_reader.go.
vp_kinds_go() {
  awk '/VP_KINDS_START/{p=1;next} /VP_KINDS_END/{p=0} p' "$STDIN_READER" \
    | grep -oE 'case "[^"]+"' \
    | grep -oE '"[^"]+"' | tr -d '"' | sort -u
}
# TS: the quoted string literals of the VIEWPOINT_KINDS array (the single source
# for viewpoint sub-kinds), bounded by the VP_KINDS_START / VP_KINDS_END sentinels
# in messages.ts. Sentinels are required because bare `"..."` literals appear all
# over the file; inside the sentinels the only quoted strings are the kind names.
vp_kinds_ts() {
  awk '/VP_KINDS_START/{p=1;next} /VP_KINDS_END/{p=0} p' "$MESSAGES_TS" \
    | grep -oE '"[^"]+"' | tr -d '"' | sort -u
}

GO_VP=$(vp_kinds_go)
TS_VP=$(vp_kinds_ts)
MISSING_IN_GO=$(comm -23 <(echo "$TS_VP") <(echo "$GO_VP"))
MISSING_IN_TS=$(comm -13 <(echo "$TS_VP") <(echo "$GO_VP"))
if [[ -n "$MISSING_IN_GO" ]]; then
  while IFS= read -r k; do
    [[ -z "$k" ]] && continue
    echo "  viewpoint kind in messages.ts but not switched in stdin_reader.go: \"$k\""
    HITS=$((HITS + 1))
  done <<< "$MISSING_IN_GO"
fi
if [[ -n "$MISSING_IN_TS" ]]; then
  while IFS= read -r k; do
    [[ -z "$k" ]] && continue
    echo "  viewpoint kind switched in stdin_reader.go but not declared in messages.ts: \"$k\""
    HITS=$((HITS + 1))
  done <<< "$MISSING_IN_TS"
fi

# --- Axis 2: the "spec" startup-line kind -----------------------------------
# Both sides must reference the literal. Presence check (not value extraction):
# the producer marshals Kind:"spec"; the consumer compares kind === "spec".
if ! grep -q 'Kind: "spec"' "$LOADER_GO"; then
  echo "  producer literal missing: loader.go no longer emits Kind: \"spec\""
  HITS=$((HITS + 1))
fi
if ! grep -q '"spec"' "$RUN_COMMAND_TS"; then
  echo "  consumer literal missing: runCommand.ts no longer recognizes \"spec\""
  HITS=$((HITS + 1))
fi

if [[ $HITS -eq 0 ]]; then
  echo "bridge-literal-parity: clean (viewpoint sub-kinds + \"spec\" line in parity)"
  exit 0
fi

echo ""
echo "bridge-literal-parity: $HITS divergence(s) found"
exit 1
