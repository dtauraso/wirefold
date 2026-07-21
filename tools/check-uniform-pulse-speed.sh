#!/usr/bin/env bash
set -euo pipefail

# Enforces UNIFORM PULSE SPEED on the production path.
#
# Doctrine: pulse speed is uniform across all wires; per-wire `speed` is rejected.
# The TS layer already cannot express it (no speed prop in wire-defs.ts WireProps).
# On the Go side, PacedWire keeps a per-instance pulseSpeed field — but that field is a
# TEST affordance: the lean per-node tests construct wires in ms units
# (NewPacedWire(latMs*PulseSpeedWuPerMs, PulseSpeedWuPerMs)) so ticksToCross == latMs.
#
# What actually keeps production uniform is that there is exactly ONE non-test
# NewPacedWire call site, and it passes the one canonical constant. That is a real
# structural invariant — but nothing failed when it stopped being true. Now something does.
#
# This guard asserts:
#   1. exactly ONE non-test NewPacedWire(...) call site exists, and
#   2. it passes PulseSpeedWuPerTick as the speed argument.
#
# A second production call site is the drift this catches: it is the moment "uniform" stops
# being structural and becomes a convention two places have to agree on. If you need one,
# the fix is not to add it — it is to remove the speed parameter from the production
# constructor entirely (and migrate the tests to express arcs as ticks*PulseSpeedWuPerTick).
#
# Exit 0 if clean; exit 1 with a report otherwise.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

CANONICAL_SPEED="PulseSpeedWuPerTick"

cd "$REPO_ROOT"

# All NewPacedWire CALL sites outside _test.go, excluding the func declaration itself.
#
# Enumerate via `git ls-files`, NOT `grep -r .`: a recursive scan from the repo root also
# descends into nested git worktrees (agents create them under .claude/worktrees/), which
# are full checkouts of this same tree — so the one production call site gets counted once
# per live worktree and this guard fails claiming a second call site that does not exist.
# git ls-files lists only THIS worktree's files, so there is no exclusion list to keep in
# sync as new nested-checkout locations appear (.claude/worktrees/ is gitignored, .gitignore:122).
#
# --others --exclude-standard is REQUIRED, not optional: without it this enumerates only
# TRACKED files, so a brand-new second call site in a not-yet-`git add`ed file passes
# silently — the guard goes vacuous exactly when it matters most (new code). Verified by
# dropping an untracked probe file with a second NewPacedWire call and confirming this
# fails; a bare `git ls-files` did not.
CALLS=$(git ls-files -z --cached --others --exclude-standard '*.go' \
  | xargs -0 grep -n "NewPacedWire(" -- \
  | grep -v "_test.go" \
  | grep -v "func NewPacedWire(" \
  || true)

COUNT=$(printf '%s' "$CALLS" | grep -c . || true)

# Refuse a vacuous pass: zero call sites means the constructor was renamed/removed and this
# guard is silently checking nothing. (Positive-assertion pattern, per
# check-ts-shading-from-go.sh / check-message-kind-parity.sh.)
if [[ "$COUNT" -eq 0 ]]; then
  echo "uniform-pulse-speed: EMPTY — no non-test NewPacedWire call site found." >&2
  echo "  The constructor was renamed or removed; refusing a vacuous pass." >&2
  echo "  Update CANONICAL_SPEED / the grep in $0 to match the new shape." >&2
  exit 1
fi

HITS=0

if [[ "$COUNT" -ne 1 ]]; then
  echo "uniform-pulse-speed: expected exactly 1 non-test NewPacedWire call site, found $COUNT:"
  printf '%s\n' "$CALLS" | sed 's/^/  /'
  echo ""
  echo "  Uniform pulse speed is structural ONLY while production builds wires in one place."
  echo "  A second call site makes it a convention instead. Remove the speed parameter from"
  echo "  the production constructor rather than adding another caller."
  HITS=$((HITS + 1))
fi

# The single call site must pass the canonical tick-unit constant.
if ! printf '%s' "$CALLS" | grep -q "$CANONICAL_SPEED"; then
  echo "uniform-pulse-speed: the production call site does not pass $CANONICAL_SPEED:"
  printf '%s\n' "$CALLS" | sed 's/^/  /'
  echo ""
  echo "  pulseSpeed is world-units-per-TICK (MODEL.md). PulseSpeedWuPerMs is the REPORTING"
  echo "  unit for SimLatencyMs and is NOT the clock's unit — passing it here would silently"
  echo "  run every bead at 16x the intended speed."
  HITS=$((HITS + 1))
fi

if [[ $HITS -eq 0 ]]; then
  echo "uniform-pulse-speed: clean"
  exit 0
fi

echo ""
echo "uniform-pulse-speed: $HITS violation(s) found"
exit 1
