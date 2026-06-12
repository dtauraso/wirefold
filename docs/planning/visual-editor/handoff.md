---
# Handoff

Live continuation prompt. Schema lives in
[continuation-prompt-template.md](continuation-prompt-template.md); this file is the
filled-in current state. A fresh AI session should read this first (no chat history
needed) and proceed.

---

## State at handoff (2026-06-12 — on `main`, no task in flight; persistence redesign + 3 follow-ups merged)

The topology-tree / Go-owns-persistence redesign (`task/persist-geometry-from-go-stream`)
is **merged to `main` and its branch deleted**: all 4 phases built, monolith files removed
(`topology.json`, `topology.scene.json`, `topology.inactive.json` — the last relocated to
`topology/inactive/` tree), live-verified end to end (port anchor survives reload).

Since that merge, **three follow-up branches also merged + deleted**:

1. **Editor-bringup fixes** — folded into the persistence merge (see the debugging chain
   below); kept here as record.
2. **Prebuilt-binary runner + `.go` watcher + orphan reap** (merge `6c8a1f31`, branch
   `task/prebuilt-binary-runner`). The editor now spawns a prebuilt binary at
   `<repoRoot>/.wirefold-cache/wirefold` (gitignored) instead of `go run .` — no per-launch
   re-link. A lazy staleness check (`ensureBinaryBuilt`, `runCommand.ts`) rebuilds at
   `run()` when any `.go` is newer than the binary. An eager `**/*.go` `FileSystemWatcher`
   (`extension.ts` `openTopologyEditor`, 250ms debounce) rebuilds on save via a shared
   `buildBinary()` in `src/goBuild.ts` (module-level `building` guard = wait-free coalesce
   so watcher + launch never run `go build -o` concurrently). On launch
   `killOrphanedSims()` (`goBuild.ts`) SIGKILLs leftover `wirefold` sims from prior/crashed
   sessions (matches command containing `wirefold` AND (`-topology` OR the cache path);
   excludes the active sim + ext-host pid). Single-panel assumption documented. First launch
   after a fresh checkout does a one-time `go build` (binary absent, gitignored, never
   committed); reused thereafter until a `.go` changes.
3. **Zombie-bead-on-restart fix** (merge `f40260b8`, branch `task/clear-pulses-on-restart`).
   The webview pulse/bead store (module-level `Map` in `webview/three/pulse-state.ts`,
   polled by `PulseBead` in `useFrame` — no listeners/version) had no run-boundary reset. A
   bead in-flight (past `send`, before `arrive`) when STOP killed Go survived into the next
   run as a zombie (observed on `2To3`). Fix: `clearAllPulses()` (swaps in a fresh empty
   `Map`; next `useFrame` draws none) called at the TOP of `store.load()`; since Go emits its
   startup spec → `load` on every restart, each new run wipes prior transient beads. Pause
   does NOT route through `load()`, so beads correctly persist across pause. Pure render-state
   reset at the run boundary — NO change to wire timing, `paced_wire.go`, or the bead model.
   Decided NOT to also clear on a bare stop (user declined) — stop-without-restart leaves
   in-flight beads until the next run, by choice.

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

No task in flight; new work is friction-driven from live editor use. Deferred:
external hand-edit handling (manual relaunch vs a future in-process reload op);
optional clear-on-stop (declined for now — beads linger until next run by choice);
optional eager binary build on panel open to remove even the first-launch ~0.5s.

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
