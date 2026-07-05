---
branch: task/edge-is-double-link
---

# Edge is the double link

## Problem this fixes

Dragging a node under a polar-lock equation (e.g. `(3,r)=(6,r)` about center 9,
with the nodes also coupled by double links) sends node positions flying to
~2.5 million. Root cause, confirmed by trace: an owned polar **radius** is being
**re-derived from a live, moving world position** (`cart2polar(node − center)`)
during the drag/cascade instead of staying the fixed stored offset it is supposed
to be. Against a mid-moving center, that reconstruction has loop gain > 1 and
diverges.

MODEL.md already forbids this ("the offset is STORED … NEVER re-derived as
`cart2polar(node − center)` from a live world during a cascade … a moved center
rigidly translates its satellites, offset unchanged"). The implementation drifted
from the model. The world re-derivation only exists as a *fallback for when no
stored offset and no movement link are present* — so the structural fix is to
guarantee a movement link (a stored radius) always exists, then delete the
fallback.

## The model: the edge IS the double link

Today an **edge** (carries a bead, one direction) and a **double link** /
movement link (undirected pair the lock engine reads for offsets, plus a visual
overlay toggled by `ToggleDoubleLinks`) are separate concepts. This plan collapses
them:

1. **Every regular edge is a double link** — two directed links between its
   endpoints.
2. **The bead rides the outgoing half** on its usual path — bead traversal
   behavior is unchanged.
3. **The double link is the stored radius** between its two endpoints — the offset
   the lock engine reads (`localPolarOf` → movement-link value). Because every edge
   now provides that stored offset, the lock engine *always* has a link to read
   from for any connected pair.
4. **Remove the derive-r-from-world 100%.** With a stored link offset always
   present, `cart2polar(node − center)` is never needed. A lock offset is only ever
   the stored/link value; if (degenerate) neither exists, the lock does not apply —
   it never reconstructs from world.
5. **Remove the double-link overlay.** Double links are no longer a
   separately-visualized thing — they *are* the edges. `ToggleDoubleLinks` and its
   overlay path are deleted.

The through-line: making every edge a double link *structurally* guarantees the
radius backup exists, which is what lets the world re-derivation be deleted without
locks silently no-op'ing.

## Work (single branch: `task/edge-is-double-link`)

Order, each step building green:

1. **Edge creation registers the double link.** On edge create, register both
   directed halves as the movement-link / radius source between the endpoints
   (seed the stored offset once from the endpoints' current positions at create
   time — the legitimate load/author-time world→polar seed, NOT a cascade
   re-derivation). Bead travels on the outgoing half (unchanged path).
   - Touch points to confirm against code: `registerMovementLinks` (loader.go),
     the edit/create path (`stdin_reader.go applyEdit`), and how a `movementLink`
     is currently derived from a topology edge.
2. **Remove derive-r-from-world.** Delete the three cascade fallbacks in
   `locks.go` — `localPolarOf` world fallback (~:236), and the `np`/`sp` fallbacks
   in `lockRecalc` (~:282, ~:287). When no stored offset and no movement link
   exist, return "no offset" / skip the equation; never `cart2polar` a live world.
   - Also audit the drag-path `refreshLink` re-derivation (`refreshLinksTouching`,
     node_move.go ~:1012): confirm whether it re-derives an offset against a moving
     center during the cascade, and bring it into model conformance (a moved center
     translates satellites with offset UNCHANGED).
3. **Remove the double-link overlay.** Delete `ToggleDoubleLinks` and the
   double-link overlay render/toggle path (schema overlay flag, buffer column if
   dedicated, `DoubleEdgeOverlayBuf` / `EdgeTube` double-tube branch, input codec
   `doubleLinks`). Keep parity across the overlay guards.

## Verification

- `TestPolarLockNoBlowup` must pass (the existing guard for exactly this bug
  class), plus a new headless repro: author `(3,r)=(6,r)` about center 9 with the
  double-linked edges, drive a drag of 6 via framed stdin, and assert no node's
  radius/position exceeds a sane bound and the cascade terminates at drag-end.
- Full `scripts/stop-checks.sh` green.
- Live: reload editor, re-drag 6, confirm no blow-up and that `.probe/go-debug.jsonl`
  shows no `cart2polar`-from-world re-derivation on the cascade path.

## Notes / open questions

- Confirm what a "double link" carries structurally today (`movementLink` in
  links.go: `BfromA`, `AfromB`, shared R) and that one topology edge → one
  movement link already holds (`registerMovementLinks`), so step 1 is mostly
  "make it authoritative + bead-on-outgoing" rather than new structure.
- Decide the fate of `refreshLinksTouching` (step 2 audit) — it is the only
  *live-during-drag* `cart2polar` and may be the actual blow-up source rather than
  the three "dead" cascade fallbacks; do not delete blindly, bring to model
  conformance.
- `ensureEqLinks` (equation-authored links between a center and its terms) may
  become redundant if every edge already supplies the link — evaluate, don't
  assume.
