#!/usr/bin/env bash
# check-doc-drift.sh — thin wrapper so scripts/audit-doc-drift.mjs (broken
# file/path references in tracked docs AND in .go/.ts/.tsx source comments)
# runs in the discovered tools/check-*.sh guard loop (scripts/stop-checks.sh
# globs that dir only). Without this wrapper the audit script existed but
# nothing ever invoked it — see CLAUDE.md drift checklist item 1 ("can the
# model skip a required step and still pass?").
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
node "$REPO_ROOT/scripts/audit-doc-drift.mjs"
