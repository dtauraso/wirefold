#!/usr/bin/env bash
# check-channel-names.sh — thin wrapper so scripts/audit-channel-names.sh (the
# CLAUDE.md channel-naming-encodes-endpoints convention) runs in the discovered
# tools/check-*.sh guard loop (scripts/stop-checks.sh globs that dir only).
# Without this wrapper the audit script existed but nothing ever invoked it.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
bash "$REPO_ROOT/scripts/audit-channel-names.sh"
