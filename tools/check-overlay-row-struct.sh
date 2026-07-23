#!/usr/bin/env bash
set -euo pipefail

# check-overlay-row-struct.sh — guards the overlay-flag transposition fix.
#
# The Overlay block's eight boolean flags used to be hand-listed in FIVE separate places
# (struct, field map, a central dispatcher's switch case list, and SetOverlayRow's nine
# same-typed positional uint8/uint32 args) inside the now-deleted Buffer.SnapshotState
# accumulator (memory/feedback_no_single_writer_bridge.md's final step). Two adjacent same-typed positional
# args could be transposed there and the mistake would COMPILE, type-check, and pass tests
# — it would surface only as the wrong overlay toggling live in the editor.
#
# The fix (tools/gen-node-defs) generates SetOverlayRow to take ONE named-field OverlayRow
# struct value instead of nine positional scalars, so every writer (Buffer/view_stream_frame.go's
# BuildViewStreamFrame today) passes a single named struct rather than a positional run.
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

if [[ $FAIL -ne 0 ]]; then
  exit 1
fi

echo "check-overlay-row-struct: clean"
exit 0
