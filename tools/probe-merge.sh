#!/usr/bin/env bash
set -euo pipefail

# Merge and sort .probe/ JSONL files by ts_ms for AI-readable diagnostics. THIS TOOL is
# where merging happens (a read-time view) — the writers never merge on write: each
# per-owner stream (view/node/edge/interior) writes its own file (memory/
# feedback_no_single_writer_bridge.md — N owners, N logs).
#
# Usage:
#   probe-merge.sh              — all files sorted by .ts_ms
#   probe-merge.sh --errors     — go-errors.jsonl + ts-errors.jsonl sorted
#   probe-merge.sh --step N     — lines with .step==N across all files sorted
#   probe-merge.sh --go         — go.jsonl (VIEW-bucket) + go-node/go-edge/go-interior.jsonl
#                                 (per-owner decentralized events) + go-errors.jsonl, sorted
#   probe-merge.sh --debug      — DEBUG BREADCRUMB channel only: buffer-decoded events
#                                 (from go.jsonl/go-node/go-edge/go-interior.jsonl) whose
#                                 .debug field is true (task/breadcrumbs-binary-buffer —
#                                 breadcrumbs are no longer a separate go-debug.jsonl JSON
#                                 stdout line; they ride the same buffer-decoded EVENT
#                                 rows as every other trace kind, tagged kind="breadcrumb"
#                                 + debug=true)
#   probe-merge.sh --ts         — ts.jsonl + ts-errors.jsonl sorted
#
# Missing files are treated as empty; requires jq.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
PROBE_DIR="$REPO_ROOT/.probe"

# These names are the canonical .probe/ log files. The TS writers source them from
# tools/topology-vscode/src/probe-files.ts (PROBE_FILES); shell can't import it, so they
# are duplicated here — keep both in sync on any rename. go.jsonl is the VIEW-bucket
# (kinds not yet decentralized to their own owner fd, PLUS main.go's own breadcrumbs,
# which have no per-node stream); go-node/go-edge/go-interior.jsonl are the genuinely
# decentralized per-owner-KIND logs — each written independently, never merged on write.
GO_FILE="$PROBE_DIR/go.jsonl"
GO_NODE_FILE="$PROBE_DIR/go-node.jsonl"
GO_EDGE_FILE="$PROBE_DIR/go-edge.jsonl"
GO_INTERIOR_FILE="$PROBE_DIR/go-interior.jsonl"
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
      read_file "$GO_NODE_FILE"
      read_file "$GO_EDGE_FILE"
      read_file "$GO_INTERIOR_FILE"
      read_file "$GO_ERR_FILE"
      read_file "$TS_FILE"
      read_file "$TS_ERR_FILE"
    } | jq -s --argjson step "$STEP" '[.[] | select(.step == $step)] | sort_by(.ts_ms) | .[]' -c
    ;;
  --go)
    merge_and_sort "$GO_FILE" "$GO_NODE_FILE" "$GO_EDGE_FILE" "$GO_INTERIOR_FILE" "$GO_ERR_FILE"
    ;;
  --debug)
    # DEBUG BREADCRUMB channel: buffer-decoded events tagged kind="breadcrumb" with
    # debug==true, across every per-owner log (a breadcrumb can ride any of them —
    # VIEW/node/edge/interior — depending which goroutine emitted it).
    {
      read_file "$GO_FILE"
      read_file "$GO_NODE_FILE"
      read_file "$GO_EDGE_FILE"
      read_file "$GO_INTERIOR_FILE"
    } | jq -s '[.[] | select(.kind == "breadcrumb" and .debug == true)] | sort_by(.ts_ms) | .[]' -c
    ;;
  --ts)
    merge_and_sort "$TS_FILE" "$TS_ERR_FILE"
    ;;
  "")
    merge_and_sort "$GO_FILE" "$GO_NODE_FILE" "$GO_EDGE_FILE" "$GO_INTERIOR_FILE" "$GO_ERR_FILE" "$TS_FILE" "$TS_ERR_FILE"
    ;;
  *)
    echo "Unknown option: $MODE" >&2
    echo "Usage: probe-merge.sh [--errors | --step N | --go | --debug | --ts]" >&2
    exit 1
    ;;
esac
