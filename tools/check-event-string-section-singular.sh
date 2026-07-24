#!/usr/bin/env bash
set -euo pipefail

# check-event-string-section-singular.sh — guards the breadcrumb-as-binary-event schema
# discipline (task/breadcrumbs-binary-buffer).
#
# THE DISCIPLINE THIS DEFENDS
# Breadcrumbs became structured EVENT rows in bufLayoutEvent (Buffer/layout.go) instead of
# free-form JSON. The agreed model is: one wide, sparse event row whose columns are REUSED
# across event kinds (Value/X/Y/Z/NodeRow/... mean different things per Kind, with -1/0
# sentinels), plus AT MOST ONE free-form text escape hatch — a single string section
# addressed by an Off/Len uint32 pair (the LabelOff/LabelLen pattern from the Node block).
#
# THE DRIFT THIS CATCHES
# The tempting-but-wrong move, when a new breadcrumb payload does not obviously fit an
# existing typed column, is to bolt ANOTHER string section onto the event row (a second
# *Off/*Len uint32 pair) — and then a third. That reintroduces exactly the per-site,
# schema-churning, opaque-string sprawl the binary conversion removed: strings that "don't
# fit the current types" get their own columns instead of being either (a) reused through an
# existing typed column or (b) funneled through the single sanctioned free-form text section.
#
# THE RULE
# bufLayoutEvent may declare AT MOST ONE string section — i.e. at most one field matching
# `<Name>Off uint32`. Zero is fine (before the text column lands); one is the sanctioned
# escape hatch; two or more is the drift, and fails here. New per-payload data must reuse an
# existing typed column or ride the single free-form text section — never a new string blob.
#
# check-generated.sh / check-buffer-layout-parity.sh do NOT catch this: a second string
# section is internally self-consistent and regenerates cleanly. This guard is the only thing
# asserting the SINGULAR-string-section invariant.
#
# Exit 0 if clean; exit 1 with a report otherwise.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
LAYOUT_FILE="$REPO_ROOT/Buffer/layout.go"

if [[ ! -f "$LAYOUT_FILE" ]]; then
  echo "check-event-string-section-singular: MISCONFIGURED — file not found: $LAYOUT_FILE" >&2
  exit 1
fi

# Isolate the bufLayoutEvent struct body (from its `type ... struct {` to the closing `}`).
BODY="$(awk '
  /^type bufLayoutEvent struct \{/ { grab=1; next }
  grab && /^\}/                    { grab=0 }
  grab                             { print }
' "$LAYOUT_FILE")"

if [[ -z "$BODY" ]]; then
  echo "check-event-string-section-singular: MISCONFIGURED — bufLayoutEvent struct not found"
  echo "  in $LAYOUT_FILE. If the struct was renamed, update this guard to match."
  exit 1
fi

# A string section is a `<Name>Off uint32` field (its `<Name>Len` partner rides alongside).
# Count the Off halves — that is the number of distinct free-form string sections on the row.
OFF_FIELDS="$(printf '%s\n' "$BODY" | grep -oE '^[[:space:]]*[A-Za-z0-9_]+Off[[:space:]]+uint32' || true)"
OFF_COUNT="$(printf '%s' "$OFF_FIELDS" | grep -c . || true)"

if [[ "$OFF_COUNT" -gt 1 ]]; then
  echo "check-event-string-section-singular: bufLayoutEvent declares $OFF_COUNT string sections"
  echo "  (fields matching '<Name>Off uint32'):"
  printf '%s\n' "$OFF_FIELDS" | sed 's/^[[:space:]]*/    /'
  echo
  echo "  The event row may carry AT MOST ONE free-form string section. Multiple string"
  echo "  sections reintroduce the per-payload opaque-string sprawl the binary breadcrumb"
  echo "  conversion removed. New payload data must either REUSE an existing typed column"
  echo "  (Value/X/Y/Z/NodeRow/...) or ride the single sanctioned free-form text section —"
  echo "  never a new *Off/*Len string blob. See tools/check-event-string-section-singular.sh."
  exit 1
fi

echo "check-event-string-section-singular: clean ($OFF_COUNT string section)"
exit 0
