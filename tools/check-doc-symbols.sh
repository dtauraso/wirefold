#!/usr/bin/env bash
set -euo pipefail

# check-doc-symbols.sh — fails when a COMMENT or DOC names a code symbol that does not exist.
#
# WHY THIS EXISTS
# ---------------
# An agent (and a human skimming) reads comments FIRST. Guards covered what was mechanically
# checkable; the prose around them was never checked, so it drifted freely. An audit found
# comments naming `WaitTick` (deleted; the clock has been sleep-only for some time),
# `useCameraStore` and `pump.ts` (erased with the old render path). Each pointed a reader at
# a pattern the code had deliberately moved AWAY from — the most expensive kind of wrong,
# because it reads as authoritative.
#
# Fix order here is code-first (CLAUDE.md): rather than re-reading every comment by hand,
# make the bug class fail a build.
#
# THE RULE
# --------
# A symbol named in a comment must appear SOMEWHERE in real code. We do not resolve it to a
# declaration (too language-specific, too fragile) — only prove it is not a ghost. A deleted
# symbol vanishes from code entirely, so the check is cheap and precise for its class.
#
# THE EXCEPTION THAT MAKES IT USABLE
# ----------------------------------
# This repo deliberately documents what it REJECTED — "rootMove is gone", "the retired
# arc/pulseSpeedMs/16 sample count", "edgeSeeds was removed". Naming a dead symbol is the
# whole point of those comments, and they are among the most valuable prose here: they stop
# a reader (or an AI) re-proposing a known dead end. A guard that forbade them would fight
# the practice it should protect.
#
# So a comment line naming a dead symbol is FINE when it marks it as history
# (gone|removed|retired|deleted|no longer|erased|legacy|replaced|superseded|was …). What
# gets flagged is a comment that names a ghost as if it were LIVE — which is exactly the
# WaitTick / useCameraStore failure: prose describing current behavior in terms of a symbol
# that no longer exists.
#
# SCOPE: BACKTICKED TOKENS ONLY — "if you backtick it, it must exist"
# --------------------------------------------------------------------
#   • backticked `tokens` in CLAUDE.md, MODEL.md, nodes/*/SPEC.md
#   • backticked `tokens` inside comment lines of tracked *.go / *.ts / *.tsx
#
# The backtick is the author SAYING "this is code", and that intent is what makes the check
# precise. Scanning bare prose was tried and rejected: it flagged 110 candidates at roughly
# 95 false positives to 4 real ghosts, because identifier-SHAPED prose (destNode, nodeId,
# zFar, roundTrip, PascalCase) is indistinguishable from a symbol reference without the
# author's signal. Backticked-only lands ~30 candidates with ~12 real ghosts and near-zero
# noise. An allowlist big enough to silence the bare-prose version would itself be a drifting
# snapshot that silently swallows future real ghosts — worse than no guard.
#
# Candidate shape: multi-word Pascal/camelCase identifiers only (WaitTick, useCameraStore,
# ticksToCross). Single words (Go, TS, node) and ALL-CAPS (JSON, FIFO, MODEL) are skipped —
# prose far more often than symbols. Paths/filenames are not checked here.
#
# FALSE POSITIVES: prose nouns that look like identifiers (TypeScript, OrbitControls) go in
# tools/doc-symbols-allow.txt, one per line. Add to the allowlist; do not weaken the shape
# rule — the allowlist is auditable, a loosened rule is not.
#
# RELATION TO scripts/check-dead-doc-tokens.sh: that guard is a DENYLIST of specific retired
# tokens (rf/nodes, GenericNode, PUMP_SLOT_HANDLER…) in CLAUDE.md/MODEL.md. It only catches
# what someone remembered to list — which is why WaitTick, FromLocalPolar, PanSceneSphere and
# TestPolarLockNoBlowup all sat in MODEL.md unflagged. This guard is the general rule and
# needs no list. Both are kept: the denylist also covers path-shaped and ALL_CAPS tokens that
# this one's identifier-shape rule deliberately skips.
#
# Exit 0 if clean; exit 1 with a report otherwise.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

ALLOWLIST="$SCRIPT_DIR/doc-symbols-allow.txt"

# Multi-word identifiers only: PascalCase (>=2 words) or camelCase (>=2 words).
IDENT_RE='([A-Z][a-z0-9]+([A-Z][a-z0-9]*)+|[a-z][a-z0-9]*([A-Z][a-z0-9]*)+)'

# A comment line marking a symbol as history is EXEMPT — see "THE EXCEPTION" above.
# Deliberately broad: a false exemption costs one stale comment, while a false FLAG teaches
# the next reader to delete a valuable "we tried this and it failed" note to appease a guard.
HISTORY_RE='(gone|removed|retired|deleted|erased|obsolete|legacy|superseded|replaced|no longer|used to|formerly|gutted|gone away|gone now|dead|gone\.|was |were |gone,|old |gone;|pre-|reverted|rejected|abandoned|do not re-|don.t re-|never re-|does not exist|doesn.t exist|never existed|unbuilt|no such|there is no|do not cite)'

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

# ---- 1. Corpus of identifiers that appear in REAL CODE ------------------------------------
# Tracked sources only (git ls-files keeps node_modules/out out of scope automatically).
# Strip // line comments so a ghost mentioned ONLY in comments cannot vouch for itself.
git ls-files -z '*.go' '*.ts' '*.tsx' '*.js' '*.sh' 2>/dev/null \
  | xargs -0 -r sed -e 's|//.*||' 2>/dev/null \
  | grep -oE "$IDENT_RE" \
  | sort -u > "$TMP/code.txt" || true

# ---- 2. Backticked tokens inside Go/TS comment lines --------------------------------------
# History-marked lines are dropped BEFORE extraction, so "`rootMove` is gone" exempts only
# that line's symbols, not the symbol everywhere.
git ls-files -z '*.go' '*.ts' '*.tsx' 2>/dev/null \
  | xargs -0 -r grep -hE '^[[:space:]]*(//|\*)' 2>/dev/null \
  | grep -viE "$HISTORY_RE" \
  | grep -oE '`[^`]+`' \
  | grep -oE "$IDENT_RE" \
  | sort -u > "$TMP/from_go.txt" || true

# ---- 3. Candidates backticked in the docs -------------------------------------------------
DOCS=(CLAUDE.md MODEL.md)
while IFS= read -r spec; do DOCS+=("$spec"); done < <(git ls-files 'nodes/*/SPEC.md' 2>/dev/null || true)

: > "$TMP/from_md.txt"
for d in "${DOCS[@]}"; do
  [[ -f "$d" ]] || continue
  # Only backticked spans; then only identifier-shaped tokens inside them.
  # Same history exemption as Go comments (MODEL.md documents rejected models on purpose).
  grep -viE "$HISTORY_RE" "$d" 2>/dev/null \
    | grep -oE '`[^`]+`' \
    | grep -oE "$IDENT_RE" >> "$TMP/from_md.txt" || true
done
sort -u -o "$TMP/from_md.txt" "$TMP/from_md.txt"

cat "$TMP/from_go.txt" "$TMP/from_md.txt" | sort -u > "$TMP/candidates.txt"

# ---- 4. Refuse a vacuous pass -------------------------------------------------------------
# If either set came back empty the extractor is broken (renamed dirs, missing git, a bad
# regex) and comm would happily compare nothing to nothing and "pass".
for pair in "code.txt:code-identifier corpus" "candidates.txt:comment/doc candidates"; do
  f="${pair%%:*}"; label="${pair#*:}"
  if [[ ! -s "$TMP/$f" ]]; then
    echo "doc-symbols: EMPTY extracted set for '$label' — extractor broken; refusing vacuous pass" >&2
    exit 1
  fi
done

# ---- 5. Ghosts = named in prose, absent from code -----------------------------------------
# Strip trailing #-comments FIRST (entries are annotated with why they're exempt), then
# whole-line comments and blanks, then whitespace.
# NOTE the `|| true`: grep exits 1 when an allowlist is empty (or all-comments), and under
# `set -e` that killed this script silently — a guard that cannot run always "passes".
: > "$TMP/allow.txt"
if [[ -f "$ALLOWLIST" ]]; then
  sed -e 's/#.*//' -e 's/[[:space:]]//g' "$ALLOWLIST" \
    | grep -vE '^$' \
    | sort -u > "$TMP/allow.txt" || true
fi

comm -23 "$TMP/candidates.txt" "$TMP/code.txt" > "$TMP/ghosts_raw.txt"
comm -23 "$TMP/ghosts_raw.txt" "$TMP/allow.txt" > "$TMP/ghosts.txt"

if [[ ! -s "$TMP/ghosts.txt" ]]; then
  echo "doc-symbols: clean"
  exit 0
fi

echo "doc-symbols: a BACKTICKED symbol exists nowhere in code:"
echo ""
while IFS= read -r sym; do
  echo "  ghost: \`$sym\`"
  # Show the sites so the fix is actionable without a second grep.
  git grep -nE "\`[^\`]*${sym}[^\`]*\`" -- '*.go' '*.ts' '*.tsx' CLAUDE.md MODEL.md 'nodes/*/SPEC.md' 2>/dev/null \
    | head -3 | sed 's/^/      /' || true
done < "$TMP/ghosts.txt"

COUNT=$(grep -c . "$TMP/ghosts.txt")
echo ""
echo "doc-symbols: $COUNT ghost symbol(s) found"
echo ""
echo "  Each is a comment pointing a reader at something that does not exist."
echo "  Fix by DELETING the stale claim, not by refreshing it — an unenforceable claim"
echo "  will just drift again. If the token is prose and not a symbol (a library name, a"
echo "  product), add it to tools/doc-symbols-allow.txt."
exit 1
