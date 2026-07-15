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
#   3. overlay flags— messages.ts OVERLAY_FLAG_NAMES  vs  the HAND-AUTHORED overlay-flags.ts
#                     renderer (readOverlay* reads + OverlayFlagVals keys), by cardinality.
#
# (Axis 4 — stdinGuideVisPayload fields vs OverlayState/flags — was removed when the
# attr="set" full-visibility-install path was dropped: its only TS caller, the load-time
# main.tsx push, was deleted and the generated stdinGuideVisPayload struct with it.)
#
# Sentinel comments (X_START / X_END) bound each region so the greps cannot sweep in
# unrelated literals (viewpoint sub-kinds, attr labels, trace kinds).
# Exit 0 if clean; exit 1 with a report otherwise.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

STDIN_READER="$REPO_ROOT/nodes/Wiring/stdin_reader.go"
MESSAGES_TS="$REPO_ROOT/tools/topology-vscode/src/messages.ts"
# The 3rd update-kind parity source moved from handle-message.ts's dispatch switch (removed
# when the TS→Go bridge became a binary buffer) to the shared IN_UPDATE_KINDS schema, which
# is the single TS list of edit-update entity kinds the encoders key off.
HANDLE_MSG="$REPO_ROOT/tools/topology-vscode/src/schema/input-layout.ts"
# overlay-flags.ts is the HAND-AUTHORED overlay renderer: it reflects each Go-owned overlay
# column out of the binary content buffer. Its per-flag bit reads (readOverlay*) + its
# OverlayFlagVals object literal are the TS-side consumer that must stay in sync with the
# overlay flag list (axis 3). (The old JSON-trace consumer pump.ts was removed in the
# content-buffer erase; overlay state now round-trips through the buffer.)
OVERLAY_FLAGS_TS="$REPO_ROOT/tools/topology-vscode/src/webview/three/overlay-flags.ts"

for f in "$STDIN_READER" "$MESSAGES_TS" "$HANDLE_MSG" "$OVERLAY_FLAGS_TS"; do
  if [[ ! -f "$f" ]]; then
    echo "edit-op-parity: MISCONFIGURED — file not found: $f" >&2
    exit 1
  fi
done

# Extract the lines of FILE strictly between the sentinel comment lines START and END.
#
# Markers are matched ANCHORED: a comment line containing the marker and NOTHING else.
# The previous `index($0,s)` was an unanchored substring match, and that is a trap — the
# moment any prose in the scanned file names the sentinel (e.g. a header saying "the op
# switch is fenced by EDIT_OPS_START/END", which is exactly the style stdin_reader.go and
# CLAUDE.md already use), the fence opens on that prose line and the extracted set becomes
# silently WRONG. It was armed but unexploded here.
#
# assert_nonempty does NOT protect against this: an unanchored match yields a non-empty,
# wrong set rather than an empty one. Vacuous-pass refusal is orthogonal to fence
# correctness. Same fix as check-message-kind-parity.sh, which cites this file as its model.
between() { # start end file
  awk -v s="$1" -v e="$2" '
    $0 ~ "^[ \t]*(//|#)[ \t]*" s "[ \t]*$" { p=1; next }
    $0 ~ "^[ \t]*(//|#)[ \t]*" e "[ \t]*$" { p=0 }
    p
  ' "$3"
}

# Double-quoted literal values from a stream.
quoted() { grep -aoE '"[^"]+"' | tr -d '"' | sort -u; }

# Top-level Go `case "..."` labels: exactly one leading tab (nested cases have two
# or more, so they are excluded). BSD grep lacks -P, so match with awk (\t = tab).
toplevel_case() { awk '/^\tcase "/'; }

# Refuse a vacuous pass: if a sentinel-bounded extractor returns an EMPTY set, a
# sentinel pair was deleted/renamed on that side and comm would compare empty-to-empty
# and "pass". Every extracted axis set must be non-empty. (Positive-assertion pattern,
# per check-ts-shading-from-go.sh / check-no-await-on-bridge.sh.)
assert_nonempty() { # value label
  if [[ -z "$(printf '%s' "$1" | tr -d '[:space:]')" ]]; then
    echo "edit-op-parity: EMPTY extracted set for '$2' — sentinel block missing/renamed; refusing vacuous parity pass" >&2
    exit 1
  fi
}

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
TS_OPS=$(between EDIT_MSG_START EDIT_MSG_END "$MESSAGES_TS" | grep -aoE 'op: "[^"]+"' | quoted)
GO_OPS=$(between EDIT_OPS_START EDIT_OPS_END "$STDIN_READER" | toplevel_case | quoted)
assert_nonempty "$TS_OPS" "axis1 messages.ts ops"
assert_nonempty "$GO_OPS" "axis1 stdin_reader.go ops"
report_diff "$(comm -13 <(echo "$GO_OPS") <(echo "$TS_OPS"))" "stdin_reader.go ops" \
            "$(comm -23 <(echo "$GO_OPS") <(echo "$TS_OPS"))" "messages.ts ops"

# --- Axis 2: update entity kinds (3-way) ------------------------------------
TS_KINDS=$(between EDIT_MSG_START EDIT_MSG_END "$MESSAGES_TS" | grep -aoE 'kind: "[^"]+"' | quoted)
GO_KINDS=$(between EDIT_UPDATE_KINDS_START EDIT_UPDATE_KINDS_END "$STDIN_READER" | toplevel_case | quoted)
HM_KINDS=$(between EDIT_UPDATE_KINDS_START EDIT_UPDATE_KINDS_END "$HANDLE_MSG" | quoted)
assert_nonempty "$TS_KINDS" "axis2 messages.ts update kinds"
assert_nonempty "$GO_KINDS" "axis2 stdin_reader.go update kinds"
assert_nonempty "$HM_KINDS" "axis2 handle-message.ts update kinds"
report_diff "$(comm -13 <(echo "$GO_KINDS") <(echo "$TS_KINDS"))" "stdin_reader.go kinds" \
            "$(comm -23 <(echo "$GO_KINDS") <(echo "$TS_KINDS"))" "messages.ts kinds"
report_diff "$(comm -13 <(echo "$HM_KINDS") <(echo "$TS_KINDS"))" "handle-message.ts kinds" \
            "$(comm -23 <(echo "$HM_KINDS") <(echo "$TS_KINDS"))" "messages.ts kinds"

# --- Axis 3: overlay flags → hand-authored renderer -------------------------
# Repointed (was messages.ts OVERLAY_FLAG_NAMES vs the GENERATED overlayToggles map in
# overlay_gen.go — circular, since the latter is generated from the former; flag→Go
# parity is already covered by check-generated.sh regenerate+diff and the overlay
# behavior test). The value axis 3 adds is flag→RENDERER parity: a flag added to
# OVERLAY_FLAG_NAMES but never wired into the hand-authored overlay-flags.ts renderer
# (per-flag readOverlay* bit reads + OverlayFlagVals object literal) would silently never
# reflect out of the buffer. Nothing else forces those two lists to track the flag list —
# that is this axis.
#
# CARDINALITY, not normalized-name, correspondence: the flag→buffer-column mapping is
# non-mechanical (tori→readOverlaySceneTori, overlays→readOverlayOverlaysVis) so a
# camelCase↔read-name compare would false-diverge. Counts are robust and catch the dominant
# failure (flag added/removed on one side only). The three independent hand-authored lists
# (flags, readOverlay* reads, OverlayFlagVals object keys) must have equal cardinality.
TS_FLAGS=$(between OVERLAY_FLAGS_START OVERLAY_FLAGS_END "$MESSAGES_TS" | quoted)
assert_nonempty "$TS_FLAGS" "axis3 messages.ts overlay flags"
# Per-flag buffer reads: the distinct readOverlay* function names used in overlay-flags.ts.
RENDER_READS=$(grep -aoE 'readOverlay[A-Za-z]+\(v\)' "$OVERLAY_FLAGS_TS" | sort -u)
# OverlayFlagVals object keys: property lines inside the `cachedVals = { … };` literal.
RENDER_KEYS=$(awk '/cachedVals = \{/{p=1;next} p&&/^[[:space:]]*};/{p=0} p&&/^[[:space:]]*[a-zA-Z_]+:/{print}' "$OVERLAY_FLAGS_TS" | grep -aoE '^[[:space:]]*[a-zA-Z_]+:' | sort -u)
assert_nonempty "$RENDER_READS" "axis3 overlay-flags.ts readOverlay* reads"
assert_nonempty "$RENDER_KEYS" "axis3 overlay-flags.ts OverlayFlagVals keys"
N_FLAGS=$(printf '%s\n' "$TS_FLAGS" | grep -c .)
N_READS=$(printf '%s\n' "$RENDER_READS" | grep -c .)
N_KEYS=$(printf '%s\n' "$RENDER_KEYS" | grep -c .)
if [[ "$N_FLAGS" -ne "$N_READS" || "$N_FLAGS" -ne "$N_KEYS" ]]; then
  echo "  overlay flag/renderer cardinality mismatch: OVERLAY_FLAG_NAMES=$N_FLAGS, overlay-flags reads=$N_READS, OverlayFlagVals keys=$N_KEYS"
  echo "    (a flag was added/removed in messages.ts but not wired into overlay-flags.ts's renderer, or vice versa)"
  HITS=$((HITS+1))
fi

# (Camera viewpoint sub-kinds axis removed: camera edits are produced in-process by the
# gesture FSM from raw-input and no longer cross the editor→Go seam, so there is no vp.Kind
# TS↔Go vocabulary left to keep in parity.)
# (Axis 4 — stdinGuideVisPayload fields — removed: the attr="set" path was dropped; see
# header comment above.)

if [[ $HITS -eq 0 ]]; then
  echo "edit-op-parity: clean (ops + update kinds + overlay flags in parity)"
  exit 0
fi
echo ""
echo "edit-op-parity: $HITS divergence(s) found"
exit 1
