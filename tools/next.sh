#!/usr/bin/env bash
# next.sh — the live "what's next" view, derived from git (no hand-synced doc).
#
# Replaces the old docs/planning/visual-editor/handoff.md, which kept drifting
# because it was a manually-updated cache of state that lives authoritatively in
# git / memory / MODEL.md. This script DERIVES state instead of storing it:
#
#   - Open work  = the task/* branches, each with its one-line git branch
#                  description (set via `git config branch.<name>.description`).
#                  Merge + delete a branch and its item disappears on its own.
#   - History    = `git log` (recent merges) + docs/planning/visual-editor/session-log.md
#   - Doctrine   = MODEL.md, CLAUDE.md, memory/MEMORY.md (read those; not repeated here)
#
# A fresh session should: run this, then read MEMORY.md and MODEL.md.

set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

bold() { printf '\033[1m%s\033[0m\n' "$1"; }

bold "current branch"
git rev-parse --abbrev-ref HEAD
echo

bold "open work (task/* branches — description = the item)"
found=0
for ref in $(git for-each-ref --format='%(refname:short)' refs/heads/'task/*'); do
  found=1
  desc=$(git config "branch.$ref.description" || true)
  printf '  \033[36m%s\033[0m\n' "$ref"
  if [ -n "$desc" ]; then
    printf '%s\n' "$desc" | fold -s -w 76 | sed 's/^/      /'
  else
    printf '      (no description — set with: git config branch.%s.description "...")\n' "$ref"
  fi
  echo
done
[ "$found" = 0 ] && echo "  (no task/* branches — clean)"
echo

bold "recently merged to main (last 8)"
git log --oneline --merges -8 main 2>/dev/null || git log --oneline -8 main
echo

bold "next steps"
echo "  - read memory/MEMORY.md (durable rules + project state)"
echo "  - read MODEL.md before any Go-network / pump change"
echo "  - friction log: docs/planning/visual-editor/session-log.md"
echo "  - verify recipe: see CLAUDE.md Workflow (scripts/stop-checks.sh must exit 0)"
