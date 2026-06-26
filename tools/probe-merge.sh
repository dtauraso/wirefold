#!/usr/bin/env bash
set -euo pipefail

# Merge and sort .probe/ JSONL files by ts_ms for AI-readable diagnostics.
#
# Usage:
#   probe-merge.sh              — all four files sorted by .ts_ms
#   probe-merge.sh --errors     — go-errors.jsonl + ts-errors.jsonl sorted
#   probe-merge.sh --step N     — lines with .step==N across all four sorted
#   probe-merge.sh --go         — go.jsonl + go-errors.jsonl sorted
#   probe-merge.sh --ts         — ts.jsonl + ts-errors.jsonl sorted
#
# Missing files are treated as empty; requires jq.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
PROBE_DIR="$REPO_ROOT/.probe"

# These four names are the canonical .probe/ log files. The TS writers source
# them from tools/topology-vscode/src/probe-files.ts (PROBE_FILES); shell can't
# import it, so they are duplicated here — keep both in sync on any rename.
GO_FILE="$PROBE_DIR/go.jsonl"
GO_ERR_FILE="$PROBE_DIR/go-errors.jsonl"
TS_FILE="$PROBE_DIR/ts.jsonl"
TS_ERR_FILE="$PROBE_DIR/ts-errors.jsonl"

# Read a file as JSONL, or emit nothing if missing.
read_file() {
  local f="$1"
  if [[ -f "$f" ]]; then
    cat "$f"
  fi
}

merge_and_sort() {
  # Each argument is a file path; concatenate all, sort by .ts_ms via jq.
  local files=("$@")
  {
    for f in "${files[@]}"; do
      read_file "$f"
    done
  } | jq -s 'sort_by(.ts_ms) | .[]' -c
}

MODE="${1:-}"

case "$MODE" in
  --errors)
    merge_and_sort "$GO_ERR_FILE" "$TS_ERR_FILE"
    ;;
  --step)
    STEP="${2:?Usage: probe-merge.sh --step N}"
    {
      read_file "$GO_FILE"
      read_file "$GO_ERR_FILE"
      read_file "$TS_FILE"
      read_file "$TS_ERR_FILE"
    } | jq -s --argjson step "$STEP" '[.[] | select(.step == $step)] | sort_by(.ts_ms) | .[]' -c
    ;;
  --go)
    merge_and_sort "$GO_FILE" "$GO_ERR_FILE"
    ;;
  --ts)
    merge_and_sort "$TS_FILE" "$TS_ERR_FILE"
    ;;
  "")
    merge_and_sort "$GO_FILE" "$GO_ERR_FILE" "$TS_FILE" "$TS_ERR_FILE"
    ;;
  *)
    echo "Unknown option: $MODE" >&2
    echo "Usage: probe-merge.sh [--errors | --step N | --go | --ts]" >&2
    exit 1
    ;;
esac
