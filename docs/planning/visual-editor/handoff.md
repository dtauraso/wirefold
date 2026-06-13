---
# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md); this file is the
filled-in current state. A fresh AI session should read this first (no chat history
needed) and proceed.

---

## State at handoff (2026-06-13 — on `main`, no task in flight; inhibitRight0 + port-anchor-render + layout merges)

`main` is current; no task in flight. **Three branches merged + deleted this session:**

1. **Port-anchor render-reactivity fix** (merge `7b6d3891`, branch
   `task/fix-port-anchor-render`). `GraphNode` in `scene-content.tsx` read node geometry via
   the non-reactive `getNodeGeometry()` snapshot, so Go-streamed anchored port directions
   never re-rendered and ports reverted to default side/slot on reload. Fix: added a
   `useNodeGeometryStore((s) => s.geoms[node.id])` selector (mirrors `CameraFit`). Go side was
   already correct (reads/streams the anchor); the break was TS-only.
2. **Promote `inhibitRight0`** (merge `3a7b429f`, branch `task/add-inhibitright-node`).
   `inhibitRight0` (kind `InhibitRightGate`) moved from the inactive tree into the active
   `topology/` diagram, fed by `2.ToNext1 -> FromLeft` and `3.ToNext0 -> FromRight` (edges
   `topology/edges/2ToInhibitRight0.json`, `3ToInhibitRight0.json`). Color inherited from the
   `InhibitRightGate` NODE_DEF (`#fce4ec`/`#880e4f`) via `meta.type` — no per-node override.
   Note: the RF-era node had a distinct inhibit-port accent (`#f48fb1` on `FromRight`); the
   current generic sphere-port renderer has no per-port handle colors, so that accent is not
   reproduced (would be a render feature, not topology data).
3. **Persist layout edits** (merge `d225a73b`, branch `task/persist-layout-edits`). Persisted
   an editor layout pass — port anchors + node positions for nodes `1`,`2`,`3` and
   `inhibitRight0`, and moved `inhibitRight0`'s `ToPassed` output port to `side:top slot:0`.

**Dropped, unmerged:** `task/multi-bead-wire` (deleted local + remote). The multi-bead
PacedWire experiment was abandoned per user; the apparent "port anchors reset" regression it
seemed to cause was actually the long-standing TS reactivity bug fixed in (1) above — not
caused by that branch.

### NEXT (post-merge)

No task in flight; new work is friction-driven from live editor use.

### Settled architecture (already on main — keep short)

Go-authoritative per-goroutine model: one clock; node-move + fade + port-anchor via
`MoveDispatch` (decentralized key→inbox, zero central fan-out); node/port position,
edge-curve geometry, node radius, and interior beads all Go-streamed; TS renders Go's
stream and computes no geometry (guard: `check-ts-computes-no-geometry`). TS owns only
the SCENE (camera, gitignored `view/scene.json`). One constant pulse speed
`CurveParamPulseSpeedWuPerMs = 0.04` wu/ms (half), uniform across ALL bead animations
(wire beads AND node-1 refill slide).

### Earlier work already merged to main (background — one line each)

1. **Scene split:** `topology.json` = diagram (tracked) / `topology.scene.json` = scene
   (gitignored, camera/labels); `topology.json` no longer skip-worktree.
2. **Deterministic `view.nodes` serialization** (idempotent load→save, kills churn).
3. **Reload gating:** external `topology.json` change reloads + restarts Go only on
   STRUCTURAL change, not view-only (`structuralKey`).
4. **Node rename** `1`/`2`/`3` (edges `1To2`/`2To3`/`2FeedbackTo1`).
5. **Animated bead colored by value + torus; removed per-node value-display overlay.**
6. **4 robustness fixes:** Trace close race (stop-signal, not `close(ch)`); empty-kind
   save guard; NUL-byte → ` ` delimiter; geometry resend on webview ready.
7. **Node-1 interior:** depleting/refilling 2×2 double-buffer, value-colored sphere+torus,
   Go-streamed local offsets (children of node group), animated refill SLIDE; peek-send /
   pop-on-feedback, NO seed/bootstrap. Node body/ring center+radius Go-owned.
8. **Node-2 interior:** single centered held-value bead, empty when `held=-1`, emitted via
   injected `EmitHeldBead` closure on held-change only.
9. **Pulse speed 0.04 + slide unified** at the base speed (`interiorSlideDurationMul=1.0`).
10. **Dead-code sweep (−78 lines); `strip-branch-local-docs.sh` scans all of `docs/`.**

### PORT-DRAG (merged — gap now closed by this branch)

You can drag a **connected** edge port along its node's ring to reposition it; the edge
follows. `Port.anchor` is wired through Go (`portDir` uses `normalize(anchor)`, applied via
`MoveDispatch`); a `port-anchor` edit op flows webview → handle-message → Go stdin. The
anchor previously did **not** survive reload (TS was the sole writer; Go only READ
`topology.json`). This branch closes that gap.

### Carry-forward facts

- **Two bead trace kinds:** `node-bead` (interior, node-LOCAL offsets, children of the
  node group) and `edge-bead` (on-wire). Node geometry (center + radius + ports + interior
  beads) is Go-streamed; TS renders, computes none.
- **`topology/` tree tracked normally**; **`topology/view/scene.json` gitignored**
  (camera/labels, reconstitutes to defaults when absent).
- **Fading a load-bearing ring edge stalls the whole ring** (token dropped); unfade does
  NOT revive — restart re-seeds from node `1`'s Input init. EXPECTED model behavior.
- **Node editing requires Go alive** (positions Go-authoritative): if Go is stopped/crashed,
  NO node moves until restart.
- **Two-process editor:** extension-host changes need **Developer: Reload Window**;
  webview-only changes hot-reload on build (edges survive via geometry resend). The
  reload/restart loop for spec changes is gone — topology is a directory tree; the default
  `-topology` flag is the `topology/` dir. See `feedback_two_process_editor_reload`.
- **Runner is a prebuilt binary**, not `go run .`: the editor spawns
  `<repoRoot>/.wirefold-cache/wirefold` (gitignored). Webview-only changes hot-reload via
  the bundle watcher; `.go` changes rebuild the binary via an eager `**/*.go` watcher (and
  a lazy staleness check at `run()`). First launch after a fresh checkout does a one-time
  `go build`. Orphaned sims from crashed sessions are SIGKILLed on launch.
- **Parser/message-kind + trace-kind parity in LOCKSTEP:** changing a TS↔Go message shape
  updates `messages.ts` parser AND the Go stdin-reader together. Guards:
  `check-message-kind-parity`, `check-trace-kind-parity`. See `feedback_schema_parser_parity`.
- **A new edit op must be forwarded in THREE places:** `messages.ts`, `handle-message.ts`
  `case "edit"` per-op forward, and the Go `stdin_reader`. The port-anchor bug was a missing
  handle-message forward.
- **Subagent commit hygiene:** subagents have repeatedly swept incidental `topology.json`
  autosave churn into commits — instruct them to `git add` specific paths only, and
  spot-check net diffs before merge.
- **React Flow is fully removed;** `RF`-named code (`RFNode`/`RFEdge`/`flowToSpec`/
  `specToFlow`) was vestigial and is retired by this branch.
- **Bead-item chain rejected** (`project_wire_is_straight_line_not_chain`) — don't
  re-propose; O(N²) follow latency.
- **Port slots are `0|1|2` per side** (top/bottom/left/right each hold at most 3 ports).
  The webview parser (`parse-nodes-edges.ts`) throws a `load-error` on slot 3+, which blanks
  the whole diagram (chrome renders, graph does not; Fit does nothing). Surfaces in
  `.probe/ts-errors.jsonl` as `spec.nodes[N].outputs[M].slot: expected 0|1|2, got 3`.

### Dev-loop

- Go: `go build ./...` + `go test -race ./...`. TS (from `tools/topology-vscode/`):
  `npm run build` (rebuilds extension.js + webview.js) + `npx tsc --noEmit` +
  `npx vitest run`. Guards: `check-trace-kind-parity.sh`, `check-message-kind-parity`,
  `check-no-await-on-bridge.sh`, `check-ts-computes-no-geometry.sh`.
- Exercise editor: **Developer: Reload Window** for extension-host changes; reopen file
  for webview-only. No reload/restart loop for spec changes (topology is a directory tree).
- No merge to main without explicit sign-off. Delete merged branches without re-asking.

## ALWAYS clause

At end of session, overwrite this file with a freshly-rendered prompt tailored to the
state you're leaving the branch in, and commit on the active branch (main if no task is
in flight). Do not rely on chat history; the next AI may be a fresh model with no
transcript. The rendered handoff must itself contain this same ALWAYS clause so the loop
is self-perpetuating across sessions. Use
[continuation-prompt-template.md](continuation-prompt-template.md) as the structural
source of truth; update the template when an invariant changes.
