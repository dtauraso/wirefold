#!/usr/bin/env bash
set -euo pipefail

# Verifies the editor->Go geometry-CRUD "edit" bridge stays in parity across every
# axis below the top-level msg.Type (which check-message-kind-parity.sh covers).
# The bridge has EXACTLY THREE ops (create/update/delete); op="update" sets an
# attribute on a typed ENTITY (kind: node/edge/camera/overlays/scene). Overlay
# visibility is one named-boolean FLAG attribute per overlay. A value added on one
# side and forgotten on another silently no-ops at runtime (CLAUDE.md "Bridge
# surface"). Three axes are checked:
#
#   1. ops          — messages.ts EditMsg  vs  stdin_reader.go applyEdit op switch.
#   2. update kinds — messages.ts EditMsg  vs  stdin_reader.go applyUpdate kind switch
#                     vs  handle-message.ts update-dispatch switch (3-way).
#   3. overlay flags— messages.ts OverlayFlag union  vs  stdin_reader.go overlayToggles map.
#
# Sentinel comments (X_START / X_END) bound each region so the greps cannot sweep in
# unrelated literals (viewpoint sub-kinds, attr labels, trace kinds).
# Exit 0 if clean; exit 1 with a report otherwise.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

STDIN_READER="$REPO_ROOT/nodes/Wiring/stdin_reader.go"
MESSAGES_TS="$REPO_ROOT/tools/topology-vscode/src/messages.ts"
HANDLE_MSG="$REPO_ROOT/tools/topology-vscode/src/extension/handle-message.ts"

for f in "$STDIN_READER" "$MESSAGES_TS" "$HANDLE_MSG"; do
  if [[ ! -f "$f" ]]; then
    echo "edit-op-parity: MISCONFIGURED — file not found: $f" >&2
    exit 1
  fi
done

# Extract the lines of FILE between sentinel comments START and END (exclusive of
# neither matters — the literals live strictly inside).
between() { # file start end
  awk -v s="$1" -v e="$2" 'index($0,s){p=1;next} index($0,e){p=0} p' "$3"
}

# Double-quoted literal values from a stream.
quoted() { grep -oE '"[^"]+"' | tr -d '"' | sort -u; }

# Top-level Go `case "..."` labels: exactly one leading tab (nested cases have two
# or more, so they are excluded). BSD grep lacks -P, so match with awk (\t = tab).
toplevel_case() { awk '/^\tcase "/'; }

HITS=0
report_diff() { # label missing_in_a a_name missing_in_b b_name
  local missing_a="$1" a_name="$2" missing_b="$3" b_name="$4"
  if [[ -n "$missing_a" ]]; then
    while IFS= read -r v; do [[ -z "$v" ]] && continue
      echo "  $v: present in $b_name but missing in $a_name"; HITS=$((HITS+1)); done <<< "$missing_a"
  fi
  if [[ -n "$missing_b" ]]; then
    while IFS= read -r v; do [[ -z "$v" ]] && continue
      echo "  $v: present in $a_name but missing in $b_name"; HITS=$((HITS+1)); done <<< "$missing_b"
  fi
}

# --- Axis 1: ops ------------------------------------------------------------
TS_OPS=$(between EDIT_MSG_START EDIT_MSG_END "$MESSAGES_TS" | grep -oE 'op: "[^"]+"' | quoted)
GO_OPS=$(between EDIT_OPS_START EDIT_OPS_END "$STDIN_READER" | toplevel_case | quoted)
report_diff "$(comm -13 <(echo "$GO_OPS") <(echo "$TS_OPS"))" "stdin_reader.go ops" \
            "$(comm -23 <(echo "$GO_OPS") <(echo "$TS_OPS"))" "messages.ts ops"

# --- Axis 2: update entity kinds (3-way) ------------------------------------
TS_KINDS=$(between EDIT_MSG_START EDIT_MSG_END "$MESSAGES_TS" | grep -oE 'kind: "[^"]+"' | quoted)
GO_KINDS=$(between EDIT_UPDATE_KINDS_START EDIT_UPDATE_KINDS_END "$STDIN_READER" | toplevel_case | quoted)
HM_KINDS=$(between EDIT_UPDATE_KINDS_START EDIT_UPDATE_KINDS_END "$HANDLE_MSG" | grep -oE 'case "[^"]+"' | quoted)
report_diff "$(comm -13 <(echo "$GO_KINDS") <(echo "$TS_KINDS"))" "stdin_reader.go kinds" \
            "$(comm -23 <(echo "$GO_KINDS") <(echo "$TS_KINDS"))" "messages.ts kinds"
report_diff "$(comm -13 <(echo "$HM_KINDS") <(echo "$TS_KINDS"))" "handle-message.ts kinds" \
            "$(comm -23 <(echo "$HM_KINDS") <(echo "$TS_KINDS"))" "messages.ts kinds"

# --- Axis 3: overlay flags --------------------------------------------------
TS_FLAGS=$(between OVERLAY_FLAGS_START OVERLAY_FLAGS_END "$MESSAGES_TS" | quoted)
GO_FLAGS=$(between OVERLAY_TOGGLES_START OVERLAY_TOGGLES_END "$STDIN_READER" | grep -oE '"[^"]+":' | tr -d '":' | sort -u)
report_diff "$(comm -13 <(echo "$GO_FLAGS") <(echo "$TS_FLAGS"))" "stdin_reader.go overlay flags" \
            "$(comm -23 <(echo "$GO_FLAGS") <(echo "$TS_FLAGS"))" "messages.ts overlay flags"

# --- Axis 4: overlays attr="set" payload fields -----------------------------
# The attr="set" full-visibility restore (OverlayState ↔ stdinGuideVisPayload) is a
# DERIVED listing on the TS side (OverlayState = Record<OverlayFlag, boolean>), so its
# field set IS the overlay flag set (TS_FLAGS). On the Go side it is the json tags of
# stdinGuideVisPayload. Assert they agree so a flag added/removed in the set-path can't
# silently no-op.
GO_GUIDEVIS=$(between GUIDEVIS_FIELDS_START GUIDEVIS_FIELDS_END "$STDIN_READER" | grep -oE 'json:"[^"]+"' | sed 's/json://' | tr -d '"' | sort -u)
report_diff "$(comm -13 <(echo "$GO_GUIDEVIS") <(echo "$TS_FLAGS"))" "stdinGuideVisPayload fields" \
            "$(comm -23 <(echo "$GO_GUIDEVIS") <(echo "$TS_FLAGS"))" "messages.ts OverlayState/flags"

# --- Axis 5: camera viewpoint sub-kinds -------------------------------------
# vp.Kind discriminates the camera sub-op (set/orbit/orbit-locked/zoom/pan). TS lists
# them once in the VIEWPOINT_KINDS const (VP_KINDS sentinels); Go switches on vp.Kind
# inside its own VP_KINDS sentinels. A kind on one side only silently no-ops.
TS_VPKINDS=$(between VP_KINDS_START VP_KINDS_END "$MESSAGES_TS" | quoted)
GO_VPKINDS=$(between VP_KINDS_START VP_KINDS_END "$STDIN_READER" | grep -oE 'case "[^"]+"' | quoted)
report_diff "$(comm -13 <(echo "$GO_VPKINDS") <(echo "$TS_VPKINDS"))" "stdin_reader.go vp kinds" \
            "$(comm -23 <(echo "$GO_VPKINDS") <(echo "$TS_VPKINDS"))" "messages.ts vp kinds"

if [[ $HITS -eq 0 ]]; then
  echo "edit-op-parity: clean (ops + update kinds + overlay flags + set-payload + vp kinds in parity)"
  exit 0
fi
echo ""
echo "edit-op-parity: $HITS divergence(s) found"
exit 1
