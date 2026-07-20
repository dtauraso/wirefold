#!/usr/bin/env bash
# strip-branch-local-docs.sh <branch>
# Scans ALL of docs/ and removes docs tagged with branch: <branch> in either form within the first 10 lines:
#   Markdown frontmatter: `branch: <branch>`
#   HTML comment:         `<!-- branch: <branch> -->` (flexible inner whitespace)
# Run before merging a task branch to main.

set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "Usage: $0 <branch>" >&2
  exit 1
fi

BRANCH="$1"
DOCS_DIR="docs"

if [[ ! -d "$DOCS_DIR" ]]; then
  echo "Error: $DOCS_DIR not found. Run from repo root." >&2
  exit 1
fi

# Find files whose first 10 lines contain a branch tag in either form.
matched=()
while IFS= read -r -d '' file; do
  head10=$(head -10 "$file" 2>/dev/null || true)
  # Markdown frontmatter form: `branch: <BRANCH>` anchored at line start
  if echo "$head10" | grep -qE "^branch: ${BRANCH}$"; then
    matched+=("$file")
    continue
  fi
  # HTML comment form: `<!-- branch: <BRANCH> -->` with flexible inner whitespace
  if echo "$head10" | grep -qE "^[[:space:]]*<!--[[:space:]]*branch:[[:space:]]*${BRANCH}[[:space:]]*-->[[:space:]]*$"; then
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

# Warn (don't block) if any removed doc is still cited from outside docs/ —
# check-doc-drift.sh will fail the next stop-checks run on these, but a warning
# here means the doc-drift failure is expected and fixable in the same commit
# that stripped the doc, rather than surfacing later as a surprise.
for f in "${matched[@]}"; do
  hits=$(grep -rln --exclude-dir=node_modules --exclude-dir=.git -- "$f" . 2>/dev/null | grep -v '^\./docs/' || true)
  if [[ -n "$hits" ]]; then
    echo "WARNING: $f is still cited from:"
    echo "$hits" | sed 's/^/  /'
  fi
done
echo "Done."
