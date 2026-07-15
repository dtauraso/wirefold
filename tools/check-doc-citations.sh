#!/usr/bin/env bash
set -euo pipefail

# check-doc-citations.sh — a quoted citation of CLAUDE.md / MODEL.md must be QUOTING it.
#
# WHY THIS EXISTS
# ---------------
# The delegate-reminder hook told the model, on every audit-shaped prompt:
#
#     Per CLAUDE.md "Model routing", delegate multi-step lookups ... to a general-purpose
#     subagent with model "sonnet", rather than grinding inline on Opus.
#
# There is no "Model routing" section. There was: 66268d97 (2026-05-05) added the Delegation
# doctrine, 24de543c (2026-05-13) wrote the hook citing it — correct at the time — and
# c123b83e (2026-06-16) REMOVED the doctrine and softened the hook threshold 1->8. The hook
# kept citing the deleted section for a month, so a retired rule was being re-imposed as
# live doctrine, in the imperative, with a citation that made it look authoritative. It also
# contradicted memory/feedback_no_nested_agents (implementer, not general-purpose).
#
# That is the WaitTick bug class aimed at the agent's instructions instead of its code. A
# citation is a claim about another file, and it was checkable all along.
#
# THE RULE
# --------
# If you write CLAUDE.md "X" or MODEL.md "X" (or CLAUDE.md's "X"), then X must appear as
# literal text in that file. Matching is case-insensitive and whitespace-normalized, so
# "no blow-up, by construction" legitimately cites "**No blow-up, by construction.**".
#
# This also enforces memory/feedback_dont_invent_doctrine — "don't paraphrase a one-off note
# into a rule and cite the paraphrase as project doctrine; grep for the literal phrasing
# first". A paraphrase cannot pass: quote it or don't cite it.
#
# SCOPE: tracked *.go, *.ts, *.tsx, *.sh, *.py, *.md — excluding docs/planning/** and HTML
# docs marked <meta name="doc-status" content="historical">, which are dated snapshots whose
# citations are pinned to their moment (same exemption as check-doc-symbols.sh).
#
# Exit 0 if clean; exit 1 with a report otherwise.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

for doc in CLAUDE.md MODEL.md; do
  if [[ ! -f "$doc" ]]; then
    echo "doc-citations: MISCONFIGURED — $doc not found (renamed? a missing doc would vacuously pass)" >&2
    exit 1
  fi
done

# A citation discussing its own history ("this hook CITED X UNTIL c123b83e REMOVED it") is
# exempt. Mirrors check-doc-symbols.sh: the repo documents what it retired on purpose, and a
# guard that punished that would delete the most useful prose in the file.
HISTORY_RE='(gone|removed|retired|deleted|erased|obsolete|legacy|superseded|replaced|no longer|used to|formerly|dead|was |were |old |reverted|rejected|abandoned|does not exist|doesn.t exist|never existed|unbuilt|no such|there is no|do not cite|do not re-add|until )'

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

# Normalize a doc to one lowercase whitespace-collapsed blob so a citation spanning a line
# break, or sitting inside **bold**/`code` markup, still matches its source.
normalize() {
  tr '\n' ' ' < "$1" | tr -s '[:space:]' ' ' | tr '[:upper:]' '[:lower:]' | sed 's/[*`]//g'
}
normalize CLAUDE.md > "$TMP/claude.txt"
normalize MODEL.md  > "$TMP/model.txt"

for f in claude model; do
  if [[ ! -s "$TMP/$f.txt" ]]; then
    echo "doc-citations: EMPTY normalized text for $f — extractor broken; refusing vacuous pass" >&2
    exit 1
  fi
done

# Collect citations: <file>\t<line>\t<DOC>\t<quoted text>
#
# Files are NORMALIZED before extraction, because a raw per-line grep misses the exact case
# this guard exists for. The hook's citation looked like this in the source:
#
#     "Heads up: ... Per CLAUDE.md "
#     "\"Model routing\", delegate multi-step lookups ... "
#
# Split across concatenated string literals AND backslash-escaped, so the text
# `CLAUDE.md "Model routing"` never appears on any single line. A line-oriented regex reports
# CLEAN on the very lie that motivated the check. Verified: it did.
#
# So: join lines, drop Python/JS implicit-concat seams (`" "` between adjacent literals),
# then unescape `\"`. Line numbers come from locating the doc mention afterwards — an
# approximate line beats a precise miss.
: > "$TMP/cites.txt"
while IFS= read -r path; do
  case "$path" in
    docs/planning/*) continue ;;
    tools/check-doc-citations.sh) continue ;;  # quotes the example that motivated it
  esac
  if [[ "$path" == *.html ]] && grep -qiE '<meta[[:space:]]+name="doc-status"[[:space:]]+content="historical"' "$path" 2>/dev/null; then
    continue
  fi
  # Cheap pre-filter: skip files that never mention the docs at all.
  grep -qE '(CLAUDE|MODEL)\.md' "$path" 2>/dev/null || continue

  approx_line=$(grep -nE '(CLAUDE|MODEL)\.md' "$path" 2>/dev/null | head -1 | cut -d: -f1)
  : "${approx_line:=1}"

  # Strip LEADING comment markers per line before joining. Without this, joining a shell
  # script's wrapped comment turns
  #     # CLAUDE.md "Bridge
  #     # surface"
  # into the citation `CLAUDE.md "Bridge # surface"` — a false positive manufactured by the
  # normalizer itself. Markdown/HTML keep their '#' (headers).
  # NOTE the '%' delimiter. With '|' as the delimiter, `s|...(//|\*)...|` puts a '|' INSIDE
  # the alternation, so sed reads it as the delimiter and dies "parentheses not balanced" —
  # on every .go/.ts file. Piped through `|| true`, that failure was SILENT: those files
  # produced no output, contributed no citations, and the guard cheerfully reported clean
  # while scanning markdown only. A guard that quietly checks less than it claims is the
  # exact bug class this file exists to catch. Verified after fixing: Go/TS citations are
  # extracted again.
  case "$path" in
    *.go|*.ts|*.tsx) strip='s%^[[:space:]]*(//|\*)[[:space:]]?%%' ;;
    *.sh|*.py)       strip='s%^[[:space:]]*#[[:space:]]?%%' ;;
    *)               strip='' ;;
  esac

  # Assert the normalizer actually produced text. Without this, ANY breakage in the strip
  # expression degrades to "this file has no citations" instead of an error — which is how
  # the '|' delimiter bug above went unnoticed through a full green stop-checks run.
  if [[ -n "$strip" ]]; then
    stripped=$(sed -E "$strip" "$path" 2>/dev/null) || stripped=""
    if [[ -z "$stripped" ]]; then
      echo "doc-citations: MISCONFIGURED — normalizer produced no text for $path" >&2
      echo "  (a broken strip expression would silently skip this file; refusing that)" >&2
      exit 1
    fi
  else
    stripped=$(cat "$path")
  fi

  printf '%s' "$stripped" \
    | tr '\n' ' ' \
    | sed -e 's/"[[:space:]]*"//g' -e 's/\\"/"/g' \
    | grep -oE '.{0,110}(CLAUDE|MODEL)\.md'"'"'?s?[[:space:]]+"[^"]{3,}"' 2>/dev/null \
    | while IFS= read -r window; do
        # A citation discussing its own history is exempt — this hook's docstring explains
        # that it CITED "Model routing" until that section was removed. Same exemption as
        # check-doc-symbols.sh: deliberate history is the valuable prose, not the bug.
        if printf '%s' "$window" | grep -qiE "$HISTORY_RE"; then continue; fi
        hit=$(printf '%s' "$window" | grep -oE '(CLAUDE|MODEL)\.md'"'"'?s?[[:space:]]+"[^"]{3,}"' | tail -1)
        [[ -n "$hit" ]] || continue
        doc="${hit%%.md*}"
        quoted="${hit#*\"}"; quoted="${quoted%\"}"
        printf '%s\t%s\t%s\t%s\n' "$path" "$approx_line" "$doc" "$quoted" >> "$TMP/cites.txt"
      done || true
done < <(git ls-files '*.go' '*.ts' '*.tsx' '*.sh' '*.py' '*.md' '*.html' 2>/dev/null || true)

if [[ ! -s "$TMP/cites.txt" ]]; then
  # Zero citations repo-wide is implausible given CLAUDE.md/MODEL.md are the doctrine docs;
  # treat it as a broken extractor rather than a clean tree.
  echo "doc-citations: EMPTY citation set — the extractor found no CLAUDE.md/MODEL.md citations at all." >&2
  echo "  That is almost certainly a broken regex, not a clean repo; refusing vacuous pass." >&2
  exit 1
fi

HITS=0
while IFS=$'\t' read -r path line doc quoted; do
  case "$doc" in
    CLAUDE) blob="$TMP/claude.txt" ;;
    MODEL)  blob="$TMP/model.txt" ;;
    *) continue ;;
  esac
  needle=$(printf '%s' "$quoted" | tr -s '[:space:]' ' ' | tr '[:upper:]' '[:lower:]' | sed 's/[*`]//g')
  if ! grep -qF -- "$needle" "$blob"; then
    if [[ $HITS -eq 0 ]]; then
      echo "doc-citations: a citation quotes text that is NOT in the doc it cites:"
      echo ""
    fi
    echo "  $path:$line"
    echo "      cites $doc.md \"$quoted\""
    echo "      but that text does not appear in $doc.md"
    HITS=$((HITS + 1))
  fi
done < "$TMP/cites.txt"

if [[ $HITS -eq 0 ]]; then
  echo "doc-citations: clean ($(wc -l < "$TMP/cites.txt" | tr -d ' ') citations checked)"
  exit 0
fi

echo ""
echo "doc-citations: $HITS bad citation(s)"
echo ""
echo "  Either quote the doc verbatim, or drop the citation and state the point directly."
echo "  A paraphrase presented as a citation is how a retired rule gets re-imposed as live"
echo "  doctrine (see this script's header). If the doc changed, the citer must change too."
exit 1
