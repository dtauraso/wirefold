#!/usr/bin/env bash
set -uo pipefail

# check-stray-screenshots.sh — PreToolUse(Bash) guard.
#
# Blocks a `git commit` when a stray Screenshot*.png / "Screen Shot*.png" sits in
# the repo root, per the visual-editor screenshot convention (move under
# docs/planning/visual-editor/screenshots/ with a date-prefixed kebab name and
# reference it from session-log.md in the same commit as the work it motivates).
#
# This was previously an inline .claude/settings.json command carrying an "if":
# "Bash(git commit*)" key on the individual hook object. That field is not part
# of the documented hook schema (only "type"/"command"/"prompt"/"timeout" per
# hook, "matcher" per group) — an unrecognized key is silently ignored, so the
# check ran on EVERY Bash call under the group's "Bash" matcher, not just git
# commit. Gating on the command string INSIDE the script (reading tool_input
# from stdin, like check-no-foreground-sim.sh) is the only way to scope this to
# commits; there is no such per-hook conditional in the schema.

input="$(cat)"
cmd="$(printf '%s' "$input" | jq -r '.tool_input.command // empty')"

if [ -z "$cmd" ]; then exit 0; fi
if ! printf '%s' "$cmd" | grep -Eq '(^|[;&|[:space:]])git[[:space:]]+commit([[:space:]]|$)'; then
  exit 0
fi

files=$(find . -maxdepth 1 \( -name 'Screenshot*.png' -o -name 'Screen Shot*.png' \) 2>/dev/null | sed 's|^\./||' | sort)
if [ -n "$files" ]; then
  python3 -c "
import json, sys
files = sys.stdin.read().strip().splitlines()
msg = 'Stray screenshot(s) in repo root: ' + ', '.join(files) + '. Per the visual-editor convention, move them under docs/planning/visual-editor/screenshots/ with a date-prefixed kebab name (e.g. 2026-05-05-<topic>-N.png) and reference them from docs/planning/visual-editor/session-log.md in the same commit as the work they motivate.'
print(json.dumps({'hookSpecificOutput': {'hookEventName': 'PreToolUse', 'permissionDecision': 'deny', 'permissionDecisionReason': msg}}))
" <<< "$files"
fi
exit 0
