---
# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md); this file is the
filled-in current state. A fresh AI session should read this first (no chat history
needed) and proceed.

---

## State at handoff (2026-06-14 — branch `task/port-ring-anchors` IN FLIGHT, NOT merged — IMPLEMENTATION COMPLETE & VERIFIED)

Context: `main` at `e699cc17`. Branch `task/port-ring-anchors` redesigned node port PHYSICAL LAYOUT: replaced the directional `side`x`slot` grid (four independently-sized buckets) + ad-hoc free `anchor` vectors with ONE FLAT ARRAY of evenly-spaced ring anchors. A port is a single `anchorId` index into that array. Spec: docs/planning/visual-editor/port-ring-anchors-spec.html (6 tabs). All 5 implementation steps DONE; user confirmed it works in the editor.

### What's done & VERIFIED
- One flat array per node: N = floor(2*pi*R/(d+p)) evenly-spaced anchors; R = nodeRadius (node size, per kind) so N varies by kind; d=8, p=2 (tunable consts in port_geometry.go). Anchor i at angle i*2pi/N (XY plane).
- Per-port data: each topology port json is now `{ name, anchorId }` (side/slot/anchor removed). 14 ports migrated by snapping each port's old direction to the nearest ring index (distinct per node).
- Go: nodes/Wiring/port_geometry.go portDir resolves ONLY via the ring (ringAnchorCount/ringAnchorDir/nodeRadius/snapToRingAnchorIndex). The old side/slot/anchor branch + specPort.Side/Slot/Anchor + CurveParamSlotPct0/1/2 are DELETED. specPort.AnchorId is *int (nil => index 0).
- TS: schema (types.ts Port: anchorId, no side/slot/anchor), parser (parse-nodes-edges.ts: parses anchorId, slot-0|1|2 throw removed), render fallback (geometry-helpers.ts portDirLocal mirrors Go ring math). Authoritative geometry still Go-streamed; fallback pre-emit only.
- Editing: dragging a port reuses the existing `port-anchor` edit op (parity: messages.ts / handle-message.ts / stdin_reader.go). TS unprojects the pointer to a world-space direction and sends it (no local geometry, no anchor naming); Go snaps it to the nearest ring anchor index (snapToRingAnchorIndex), writes port.AnchorId, clears old anchor, re-streams. Same shape as node-move. Fire-and-forget; bridge still 4 ops.
- go build + go test -race green (7 pkgs); tsc clean; vitest at baseline (~67 pass + 1 pre-existing topology-data-paths failure re: missing nodes/4/data.json). Bridge/parity guards clean. User VERIFIED in editor: ports render at ring positions, network runs, drag snaps a port to a discrete anchor and the edge follows.

### Commits (this branch, newest first)
c955e09c remove dead side/slot/anchor path (ring is only model); 54ad71c7 port drag -> world target, Go snaps to anchorId; 7aecbf6a TS schema+parser+fallback on anchorId; c8dc95d3 migrate topology ports to anchorId; 52e65d49 Go ring geometry (additive). Plus the spec/handoff doc commits (b8fdabb2, d14fd2ae, 06c1b3f9, 011f26fc, e5b4f51f..da440dfc, 83a56f6f).

### Optional cleanups (non-blocking)
- Lint: unused test helper comparePortLists (loader_tree_test.go), an unused-const-set warning in curve_params.go (verify CurveParamSlotPct removal was complete), unused param isInput (port_geometry_test.go), a few modernization hints (max/range-over-int, tagged switch in stdin_reader.go). Cosmetic.
- d/p (8/2) and R (nodeRadius) are tunable by eye in the editor if anchor spacing wants adjusting.

### Ready to merge
Branch is a coherent, verified milestone. Before merge: run tools/strip-branch-local-docs.sh task/port-ring-anchors (NOTE: the spec html port-ring-anchors-spec.html is branch-local by convention but NOT frontmatter-tagged, so the script won't catch it — decide at merge whether the spec rides to main or is removed). Then --no-ff merge, push, delete branch.

### Carry-forward
- GEOMETRY IS GO-OWNED: Go computes the ring + the snap; TS only unprojects the pointer (camera/input) and renders Go's stream.
- SWARM CAUTION (this session): a background Agent dispatch fanned out into ~20 duplicate agents + a stray unrequested commit (reverted). Keep subagents FOREGROUND; spot-check git log before pushing.
- IDE diagnostics were repeatedly STALE this session (phantom broken imports / unused funcs that were actually wired). Verify against `go build` / grep, not the diagnostic panel.

### Next step
None required — feature complete & verified. Either merge (after strip-branch-local-docs + spec-rides-or-not decision) or do the optional lint cleanups first.

## ALWAYS clause

At end of session, overwrite this file with a freshly-rendered prompt tailored to the
state you're leaving the branch in, and commit on the active branch (main if no task is
in flight). Do not rely on chat history; the next AI may be a fresh model with no
transcript. The rendered handoff must itself contain this same ALWAYS clause so the loop
is self-perpetuating across sessions. Use
[continuation-prompt-template.md](continuation-prompt-template.md) as the structural
source of truth; update the template when an invariant changes.
