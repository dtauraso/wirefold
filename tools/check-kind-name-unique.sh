#!/usr/bin/env bash
set -euo pipefail

# Static duplicate-kind check: every production node package registers its kind name with a
# Wiring.Register("<Kind>", ...) call in its node.go init(). Two packages registering the
# SAME kind name is caught today only at RUNTIME — registry.go's Register panics
# ("Wiring.Register: kind already registered: X") when the second init() runs, i.e. at
# process startup, not at build. This guard moves that shape check earlier: it fails the
# build if any kind literal appears in more than one production node.go, so a collision is a
# red check instead of a startup crash.
#
# Scope is deliberately the PRODUCTION registrations (nodes/<kind>/node.go). Test files
# register their own throwaway kinds and are a separate namespace exercised by `go test`
# (which would itself panic on a dup) — not part of the shipped kind vocabulary.
#
# Exit 0 clean, exit 1 with a report.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

shopt -s nullglob
node_files=("$REPO_ROOT"/nodes/*/node.go)
shopt -u nullglob

if [ ${#node_files[@]} -eq 0 ]; then
  # No production node packages found (partial checkout) — nothing to check.
  exit 0
fi

# Pull the kind literal out of every `Wiring.Register("<Kind>", ...)` call across the
# production node.go files, then report any name that occurs more than once.
dups=$(grep -hoE 'Wiring\.Register\("[^"]+"' "${node_files[@]}" \
  | sed -E 's/.*Register\("([^"]+)"/\1/' \
  | sort | uniq -d || true)

if [ -n "$dups" ]; then
  echo "check-kind-name-unique: a kind name is registered by more than one production"
  echo "node package (registry.go would panic on this at startup). Duplicate kind(s):"
  echo "$dups" | sed 's/^/  /'
  exit 1
fi

exit 0
