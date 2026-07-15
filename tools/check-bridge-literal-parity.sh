#!/usr/bin/env bash
set -euo pipefail

# Guards hand-duplicated string literals that cross the TS<->Go boundary and
# have no generator linking them. check-message-kind-parity.sh covers top-level
# msg.Type and check-edit-op-parity.sh covers msg.Op, but one literal axis is
# below both:
#
#   - the "spec" startup line kind — Go emits {"kind":"spec",...} on startup
#     (loader.go) and TS recognizes it (runCommand.ts). Rename either side and
#     spec-load silently breaks (blank editor).
#
# Exit 0 if in parity; exit 1 with a report otherwise.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

LOADER_GO="$REPO_ROOT/nodes/Wiring/loader.go"
RUN_COMMAND_TS="$REPO_ROOT/tools/topology-vscode/src/runCommand.ts"

for f in "$LOADER_GO" "$RUN_COMMAND_TS"; do
  if [[ ! -f "$f" ]]; then
    echo "bridge-literal-parity: MISCONFIGURED — file not found: $f" >&2
    exit 1
  fi
done

HITS=0

# (Axis 1 — viewpoint sub-kinds — removed: camera edits are produced in-process by the
# gesture FSM from raw-input and no longer cross the editor→Go seam, so there is no vp.Kind
# TS↔Go vocabulary left to keep in parity.)

# --- Axis: the "spec" startup-line kind -------------------------------------
# Both sides must reference the literal. This axis is PRESENCE-only (not a value
# comparison) by design: the two sides use asymmetric syntax — Go marshals a struct
# field `Kind: "spec"` while TS compares `kind === "spec"` — so there is no single
# shared token to extract-and-diff without brittle per-side parsing. Both anchor on
# the same literal "spec", so a rename on either side drops that side's presence hit
# and trips this guard. If the syntaxes ever converge, upgrade to value extraction.
if ! grep -aq 'Kind: "spec"' "$LOADER_GO"; then
  echo "  producer literal missing: loader.go no longer emits Kind: \"spec\""
  HITS=$((HITS + 1))
fi
if ! grep -v '^\s*//' "$RUN_COMMAND_TS" | grep -q 'kind === "spec"'; then
  echo "  consumer literal missing: runCommand.ts no longer recognizes \"spec\""
  HITS=$((HITS + 1))
fi

if [[ $HITS -eq 0 ]]; then
  echo "bridge-literal-parity: clean (\"spec\" line in parity)"
  exit 0
fi

echo ""
echo "bridge-literal-parity: $HITS divergence(s) found"
exit 1
