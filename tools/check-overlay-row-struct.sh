#!/usr/bin/env bash
set -euo pipefail

# check-overlay-row-struct.sh — guards the overlay-flag transposition fix.
#
# Buffer/snapshot.go used to hand-list the Overlay block's eight boolean flags in FIVE
# separate places (struct, field map, Update's switch case list, and SetOverlayRow's nine
# same-typed positional uint8/uint32 args). Two adjacent same-typed positional args could
# be transposed there and the mistake would COMPILE, type-check, and pass tests — it would
# surface only as the wrong overlay toggling live in the editor.
#
# The fix (tools/gen-node-defs) generates SetOverlayRow to take ONE named-field OverlayRow
# struct value instead of nine positional scalars, and generates overlayFlagFieldsOf /
# IsOverlayFlagKind from the SAME Overlay-block schema in Buffer/layout.go, so
# Buffer/snapshot.go no longer hand-lists the flag set at all.
#
# check-generated.sh already catches a stale/hand-reverted regen of buffer_layout_gen.go
# in general. This guard is narrower and cheaper: it asserts the SPECIFIC invariant that
# closes the transposition hazard — SetOverlayRow's signature takes the struct, not a run
# of same-typed positional scalars — so a future hand-edit of the generator that
# reintroduces positional overlay args (and is otherwise self-consistent, so
# check-generated would stay clean) still fails here.
#
# Exit 0 if clean; exit 1 with a report otherwise.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

GEN_FILE="$REPO_ROOT/Buffer/buffer_layout_gen.go"
GENERATOR="$REPO_ROOT/tools/gen-node-defs/buffer_layout.go"

for f in "$GEN_FILE" "$GENERATOR"; do
  if [[ ! -f "$f" ]]; then
    echo "check-overlay-row-struct: MISCONFIGURED — file not found: $f" >&2
    exit 1
  fi
done

FAIL=0

if ! grep -qE '^func SetOverlayRow\(buf \[\]byte, row OverlayRow\) \{$' "$GEN_FILE"; then
  echo "check-overlay-row-struct: SetOverlayRow in $GEN_FILE is not the expected"
  echo "  'func SetOverlayRow(buf []byte, row OverlayRow)' named-struct signature."
  echo "  A positional-scalar signature reintroduces the transposition hazard."
  FAIL=1
fi

if ! grep -q 'type OverlayRow struct {' "$GEN_FILE"; then
  echo "check-overlay-row-struct: OverlayRow struct type not found in $GEN_FILE"
  FAIL=1
fi

if ! grep -q 'func overlayFlagFieldsOf(row \*OverlayRow) map\[string\]\*uint8 {' "$GEN_FILE"; then
  echo "check-overlay-row-struct: overlayFlagFieldsOf(...) not found in $GEN_FILE"
  echo "  (the generated Kind->field map Buffer/snapshot.go's Update dispatches through)"
  FAIL=1
fi

if ! grep -q 'func IsOverlayFlagKind(kind string) bool {' "$GEN_FILE"; then
  echo "check-overlay-row-struct: IsOverlayFlagKind(...) not found in $GEN_FILE"
  echo "  (the generated dispatch check Buffer/snapshot.go's Update uses instead of a"
  echo "  hand-listed switch case for the overlay-flag Kinds)"
  FAIL=1
fi

# Buffer/snapshot.go must not reintroduce a hand-authored mirror struct or a hand-listed
# Kind->field map/case list for the overlay flags.
SNAPSHOT_FILE="$REPO_ROOT/Buffer/snapshot.go"
if grep -q 'type overlaySnapState struct' "$SNAPSHOT_FILE"; then
  echo "check-overlay-row-struct: $SNAPSHOT_FILE hand-authors overlaySnapState again —"
  echo "  it should use the generated OverlayRow (buffer_layout_gen.go) directly."
  FAIL=1
fi

if [[ $FAIL -ne 0 ]]; then
  exit 1
fi

echo "check-overlay-row-struct: clean"
exit 0
