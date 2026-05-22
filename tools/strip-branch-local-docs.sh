#!/usr/bin/env bash
# strip-branch-local-docs.sh <branch>
# Removes planning docs tagged with frontmatter `branch: <branch>`.
# Run before merging a task branch to main.

set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "Usage: $0 <branch>" >&2
  exit 1
fi

BRANCH="$1"
DOCS_DIR="docs/planning"

if [[ ! -d "$DOCS_DIR" ]]; then
  echo "Error: $DOCS_DIR not found. Run from repo root." >&2
  exit 1
fi

# Find files whose first three lines form YAML frontmatter containing `branch: <BRANCH>`.
# Pattern: file starts with ---, has a line `branch: <BRANCH>`, then a closing ---.
matched=()
while IFS= read -r -d '' file; do
  # Extract up to first 10 lines to find frontmatter block
  head10=$(head -10 "$file" 2>/dev/null || true)
  first_line=$(echo "$head10" | head -1)
  if [[ "$first_line" != "---" ]]; then
    continue
  fi
  # Check for branch tag inside the frontmatter
  if echo "$head10" | grep -qE "^branch: ${BRANCH}$"; then
    matched+=("$file")
  fi
done < <(find "$DOCS_DIR" -type f \( -name "*.md" -o -name "*.html" \) -print0)

if [[ ${#matched[@]} -eq 0 ]]; then
  echo "No docs tagged with branch: $BRANCH — nothing to remove."
  exit 0
fi

echo "Removing ${#matched[@]} doc(s) tagged with branch: $BRANCH"
for f in "${matched[@]}"; do
  git rm "$f"
  echo "  removed: $f"
done
echo "Done."
