#!/usr/bin/env bash
set -euo pipefail

# check-staticcheck.sh — Go static-analysis guard (the Go equivalent of
# check-eslint.sh for the TS side).
#
# Two layers:
#   1. `go vet ./...` — ships with the toolchain, so it is ALWAYS version-matched
#      and always available. Fail hard on any nonzero exit.
#   2. `staticcheck ./...` — a dev tool that must be installed separately. Policy:
#      FOUND  → enforce (any finding fails the guard with exit 1).
#      ABSENT → warn + skip (exit 0). We don't block a checkout that merely lacks
#               the dev tool; staticcheck is enforced whenever it is present
#               (locally on dev machines, and can be added to CI).

cd "$(git rev-parse --show-toplevel)"

# --- Layer 1: go vet (always available) ---
if ! vet_out=$(go vet ./... 2>&1); then
  echo "go vet failed:" >&2
  echo "$vet_out" >&2
  exit 1
fi

# --- Layer 2: staticcheck (found → enforce, absent → warn+skip) ---
# Locate the binary: prefer one on PATH, else the conventional GOPATH/bin.
staticcheck_bin=""
if command -v staticcheck >/dev/null 2>&1; then
  staticcheck_bin="$(command -v staticcheck)"
elif [ -x "$(go env GOPATH)/bin/staticcheck" ]; then
  staticcheck_bin="$(go env GOPATH)/bin/staticcheck"
fi

if [ -z "$staticcheck_bin" ]; then
  echo "staticcheck not installed; skipping (install with:" >&2
  echo "  go install honnef.co/go/tools/cmd/staticcheck@latest)" >&2
  exit 0
fi

# Present → enforce. Any finding is printed and fails the guard.
if ! sc_out=$("$staticcheck_bin" ./... 2>&1); then
  echo "staticcheck reported findings:" >&2
  echo "$sc_out" >&2
  exit 1
fi

exit 0
