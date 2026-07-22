#!/usr/bin/env bash
set -euo pipefail

# Overlay wire-vocabulary name parity: the overlay FLAG names authored in messages.ts
# (OVERLAY_FLAG_NAMES, the single TS source) must be exactly the key set of the Go
# overlayToggles map (overlay_gen.go) — the attr="toggle" wire name → flip-method table
# stdin_reader.go dispatches on. A name present on one side and not the other silently
# no-ops the toggle at runtime.
#
# overlay_gen.go IS generated from OVERLAY_FLAG_NAMES (tools/gen-node-defs), so
# check-generated.sh already catches a stale regen by full-file diff. This guard is the
# NAMED, boundary-specific complement: it fails with "these exact flag names diverge"
# instead of "the generated file is stale", and it pins the contract at the wire-name set
# itself so a future refactor that changed how the file is generated can't quietly drop it.
# Both regions are sentinel-fenced so the extraction can't sweep in unrelated literals.
#
# Exit 0 clean, exit 1 with a report.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
TS="$REPO_ROOT/tools/topology-vscode/src/messages.ts"
GO="$REPO_ROOT/nodes/Wiring/overlay_gen.go"

if [ ! -f "$TS" ] || [ ! -f "$GO" ]; then
  # Partial checkout — nothing to compare, not a failure.
  exit 0
fi

# messages.ts: the quoted strings between OVERLAY_FLAGS_START / OVERLAY_FLAGS_END.
ts_names=$(awk '/OVERLAY_FLAGS_START/{on=1;next} /OVERLAY_FLAGS_END/{on=0} on' "$TS" \
  | grep -oE '"[^"]+"' | tr -d '"' | sort)

# overlay_gen.go: the map KEYS between OVERLAY_TOGGLES_START / OVERLAY_TOGGLES_END
# (the quoted string before the ':' on each entry line).
go_names=$(awk '/OVERLAY_TOGGLES_START/{on=1;next} /OVERLAY_TOGGLES_END/{on=0} on' "$GO" \
  | grep -oE '"[^"]+"[[:space:]]*:' | grep -oE '"[^"]+"' | tr -d '"' | sort)

if [ "$ts_names" != "$go_names" ]; then
  echo "check-overlay-flag-name-parity: OVERLAY_FLAG_NAMES (messages.ts) and the"
  echo "overlayToggles keys (overlay_gen.go) diverge. Diff (< messages.ts, > overlay_gen.go):"
  diff <(printf '%s\n' "$ts_names") <(printf '%s\n' "$go_names") || true
  echo "If you changed the overlay vocabulary, edit OVERLAY_FLAG_NAMES in messages.ts and"
  echo "regenerate (go run ./tools/gen-node-defs) so overlay_gen.go matches."
  exit 1
fi

exit 0
