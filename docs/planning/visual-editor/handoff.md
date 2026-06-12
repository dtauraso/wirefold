---
# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md); this file is the
filled-in current state. A fresh AI session should read this first (no chat history
needed) and proceed.

---

## State at handoff (2026-06-12 — on `task/persist-geometry-from-go-stream`, COMPLETE + LIVE-VERIFIED, merging now)

Active branch **`task/persist-geometry-from-go-stream`** — all 4 phases built, monolith
files removed (`topology.json`, `topology.scene.json`, `topology.inactive.json` — the last
relocated to `topology/inactive/` tree), LIVE-VERIFIED end to end. This session is doing
the merge to `main`.

### Editor-bringup debugging chain (after build, all real fixes)

1. **Startup load-race:** Go's one-shot spec line was posted before the webview listener
   attached → host now caches `lastSpec` and replays `"load"` on webview `"ready"` (`extension.ts`).
2. **Dead document-gate:** `handle-message.ts` still gated webview-log (and breadcrumbs) on
   the removed custom-editor `document` → threaded a `logUri` into `MessageCtx`, removed
   the gates; restored ts-webview logging.
3. **parseSpec schema mismatch:** tree-based `EmitSpecLine` dropped port `kind` and edge
   `id` that `parseSpec` required → Go now emits edge `id`; `specNode.Position` got
   `json:"-"`; `parsePort` defaults `kind`; added a load-error breadcrumb (in
   `ERROR_LABELS`) so silent `store.load` throws surface to `ts-errors.jsonl`.
4. **Auto-fit not firing / framing wrong:** `CameraFitter` only depended on `loadEpoch` and
   bailed if nodes/canvas not ready, and negated `cy` while `nodeWorldPos` was y-up → fixed
   to fit once-per-epoch gated on Go geometry present for all nodes, dropped the y negation
   to match manual Fit (HomeButton). Diagram now appears framed on open.
5. **Compact JSON writes:** `tree_writer` now emits compact single-line JSON matching the
   fixtures.

### Live verification (this session)

Editor opens framed; dragging a node wrote `topology/view/nodes/<id>.json`; dragging a
port anchor wrote `topology/nodes/<id>/inputs/<port>.json` with an `"anchor"` field; on
reload Go re-read the tree and the spec stream carried both the moved position AND the port
anchor — the original branch goal (port anchor survives reload) is confirmed working.

### NEXT (post-merge)

Branch merged + deleted this session; new work is friction-driven from live editor use.
Deferred follow-ups: external hand-edit handling (manual relaunch vs a future in-process
reload op); long-running suspended dev `go run` processes from earlier sessions exist —
restart the live editor against the merged build to pick up compact writes.

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
