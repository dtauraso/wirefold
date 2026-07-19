#!/usr/bin/env bash
set -euo pipefail

# check-spec-format-view-fields.sh — nodes/SPEC-FORMAT.md's `## View` field table must name
# exactly the view.* fields tools/gen-node-defs's parseSpecMD (now in ast_parse.go) actually
# reads (via vmap["<field>"]) — no more, no less.
#
# WHY THIS EXISTS
# ---------------
# SPEC-FORMAT.md is the authoring contract for nodes/<Kind>/SPEC.md, but no generator or guard
# reads it — it is pure prose describing what the generator does, so nothing stopped it from
# drifting. An audit found it documenting three View fields (accent, displays, defaultLabel)
# that main.go parsed into viewDef but never emitted anywhere downstream (dead — confirmed by
# regenerating node-defs.ts after deleting them: zero diff), while omitting six fields
# (role, shape, fill, stroke, width, height) that main.go DOES read and that DO reach
# node-defs.ts / node_dims_gen.go.
#
# THE RULE
# --------
# Extract the field names from main.go's `vmap["<name>"]` reads inside parseSpecMD (the ground
# truth of what the generator actually parses from a SPEC.md `## View` table) and compare them,
# as a set, to the field names appearing in the first column of SPEC-FORMAT.md's `## View`
# field-value tables. Any asymmetric difference fails the build:
#   - a field the doc documents but the generator does not read → doc re-added a dead/ghost
#     field (the accent/displays/defaultLabel class).
#   - a field the generator reads but the doc omits → the generator grew a field the authoring
#     contract never mentioned (an author would have no way to discover it exists).
#
# This is intentionally narrower than "does the field affect rendering" (main.go parses fields
# it discards downstream too, e.g. accent WAS parsed before this fix) — it only proves
# doc-vs-parser parity, which is what SPEC-FORMAT.md claims to describe. It cannot silently
# regress: reintroducing a dead field to the doc without touching main.go, or adding a new
# vmap read to main.go without documenting it, both flip this guard red.
#
# Exit 0 if clean; exit 1 with a diff otherwise.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

GEN_DIR="tools/gen-node-defs"
DOC="nodes/SPEC-FORMAT.md"

if [[ ! -d "$GEN_DIR" ]]; then
  echo "check-spec-format-view-fields: MISSING $GEN_DIR — cannot verify parity, refusing vacuous pass" >&2
  exit 1
fi
if [[ ! -f "$DOC" ]]; then
  echo "check-spec-format-view-fields: MISSING $DOC — cannot verify parity, refusing vacuous pass" >&2
  exit 1
fi

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

# ---- fields the generator actually reads (vmap["field"] inside parseSpecMD) ----------------
# Scan every non-test *.go file in $GEN_DIR, not just main.go — parseSpecMD (and its
# vmap[...] reads) may live in any file within this package after a split, and a
# single-file-path grep here would silently stop tracking new/moved reads.
grep -hoE 'vmap\["[A-Za-z0-9_]+"\]' "$GEN_DIR"/*.go \
  | sed -E 's/vmap\["([A-Za-z0-9_]+)"\]/\1/' \
  | sort -u > "$TMP/code_fields.txt"

if [[ ! -s "$TMP/code_fields.txt" ]]; then
  echo "check-spec-format-view-fields: EMPTY vmap[...] extraction from $GEN_DIR/*.go — extractor broken, refusing vacuous pass" >&2
  exit 1
fi

# ---- fields SPEC-FORMAT.md documents in its `## View` table(s) -----------------------------
# Both the `## File layout` skeleton and the worked `## View section` example carry a
# `| Field | Value |` table; collect field names from every such table in the doc. A table row
# looks like `| kind | <rfType> |` — take the first cell after splitting on `|`, skip the
# header row and the `----` separator row.
awk '
  /^\| *Field *\| *Value *\|/ { intable=1; next }
  intable && /^\|[-: ]+\|[-: ]+\|/ { next }
  intable && /^\|/ {
    line=$0
    sub(/^\|/, "", line)
    split(line, parts, "|")
    field=parts[1]
    gsub(/^[ \t]+|[ \t]+$/, "", field)
    if (field != "") print field
    next
  }
  intable && !/^\|/ { intable=0 }
' "$DOC" | sort -u > "$TMP/doc_fields.txt"

if [[ ! -s "$TMP/doc_fields.txt" ]]; then
  echo "check-spec-format-view-fields: EMPTY field extraction from $DOC — extractor broken, refusing vacuous pass" >&2
  exit 1
fi

DOC_ONLY=$(comm -23 "$TMP/doc_fields.txt" "$TMP/code_fields.txt" || true)
CODE_ONLY=$(comm -13 "$TMP/doc_fields.txt" "$TMP/code_fields.txt" || true)

if [[ -z "$DOC_ONLY" && -z "$CODE_ONLY" ]]; then
  echo "check-spec-format-view-fields: clean"
  exit 0
fi

echo "check-spec-format-view-fields: $DOC's ## View field table is out of parity with"
echo "$GEN_DIR's parseSpecMD vmap[...] reads:"
echo ""
if [[ -n "$DOC_ONLY" ]]; then
  echo "  documented in $DOC but NOT read by the generator (dead/ghost field — remove from the doc, or if it should do something, wire it into main.go):"
  echo "$DOC_ONLY" | sed 's/^/    - /'
fi
if [[ -n "$CODE_ONLY" ]]; then
  echo "  read by the generator (vmap[\"...\"]) but NOT documented in $DOC (undiscoverable field — document it, or if vestigial, delete the vmap read):"
  echo "$CODE_ONLY" | sed 's/^/    - /'
fi
echo ""
exit 1
